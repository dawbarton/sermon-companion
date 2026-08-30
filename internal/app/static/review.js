"use strict";
const {api, formatTime, parseTime} = window.SC;
const elementIDs = [
  "session-list", "empty", "detail", "session-title", "session-meta", "audio",
  "waveform-viewport", "waveform-canvas", "segment-overlay", "playhead",
  "waveform-loading", "waveform-range", "pan-left", "pan-right", "zoom-in",
  "zoom-out", "zoom-full", "segments", "markers", "add-marker", "marker-label",
  "marker-kind", "marker-time", "export", "download", "export-status", "error"
];
const elements = Object.fromEntries(elementIDs.map(id => [id, document.getElementById(id)]));

let current = null;
let sessions = [];
let playingSegmentID = null;
let waveform = emptyWaveform();
let dragState = null;

function emptyWaveform() {
  return {sessionID: null, peaks: null, pointsPerSecond: 20, duration: 0, viewStart: 0, viewEnd: 1, loading: false};
}

async function loadSessions() {
  try {
    sessions = await api("/api/sessions");
    elements["session-list"].replaceChildren(...sessions.map(session => {
      const button = document.createElement("button");
      button.className = `session-card${current?.id === session.id ? " selected" : ""}`;
      const title = document.createElement("strong"); title.textContent = session.title;
      const meta = document.createElement("small"); meta.textContent = `${new Date(session.startedAt).toLocaleDateString()} · ${session.status}`;
      button.append(title, meta); button.addEventListener("click", () => selectSession(session.id)); return button;
    }));
    if (!current && sessions.length) await selectSession(sessions[0].id);
  } catch (error) { showError(error); }
}

async function selectSession(id) {
  stopSegmentPlayback();
  try {
    current = await api(`/api/sessions/${id}`);
    waveform = emptyWaveform();
    render();
    void loadWaveform(id);
    await loadSessions();
  } catch (error) { showError(error); }
}

async function loadWaveform(id) {
  waveform.sessionID = id;
  waveform.loading = true;
  waveform.duration = Math.max(current?.durationSeconds || 0, 1);
  waveform.viewStart = 0;
  waveform.viewEnd = waveform.duration;
  renderWaveform();
  try {
    const data = await api(`/api/sessions/${id}/waveform`);
    if (current?.id !== id) return;
    const binary = atob(data.peaksBase64);
    const peaks = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) peaks[i] = binary.charCodeAt(i);
    const duration = Math.max(data.durationSeconds || 0, current.durationSeconds || 0, 1);
    waveform = {sessionID: id, peaks, pointsPerSecond: data.pointsPerSecond, duration, viewStart: 0, viewEnd: duration, loading: false};
    renderWaveform();
  } catch (error) {
    if (current?.id !== id) return;
    waveform.loading = false;
    elements["waveform-loading"].textContent = `Waveform unavailable: ${error.message}`;
    elements["waveform-loading"].classList.remove("hidden");
  }
}

function render() {
  elements.empty.classList.toggle("hidden", !!current);
  elements.detail.classList.toggle("hidden", !current);
  if (!current) return;
  elements["session-title"].textContent = current.title;
  elements["session-meta"].textContent = `${new Date(current.startedAt).toLocaleString()} · ${formatTime(current.durationSeconds, true)} · ${current.status}`;
  const audioURL = `/api/sessions/${current.id}/audio`;
  if (!elements.audio.src.endsWith(audioURL)) elements.audio.src = audioURL;
  renderSegmentRows();
  elements.markers.replaceChildren(...[...current.markers].sort((a,b) => a.atSeconds-b.atSeconds).map(markerRow));
  renderWaveform();
  const exp = current.export;
  elements["export-status"].textContent = exp ? exp.status === "running" ? "Creating MP3…" : exp.status === "failed" ? `Export failed: ${exp.error}` : "MP3 ready." : "";
  elements.export.disabled = current.status === "recording" || exp?.status === "running";
  elements.download.classList.toggle("hidden", exp?.status !== "completed");
  if (exp?.status === "completed") elements.download.href = `/api/sessions/${current.id}/export-file`;
}

function renderSegmentRows() {
  elements.segments.replaceChildren(...[...current.segments].sort((a,b) => a.startSeconds-b.startSeconds).map(segmentRow));
}

function segmentRow(segment) {
  const row = document.createElement("tr");
  row.dataset.segmentId = segment.id;
  const play = document.createElement("button");
  play.className = "segment-play";
  play.textContent = playingSegmentID === segment.id ? "Stop" : "Play";
  play.disabled = segment.endSeconds == null;
  play.addEventListener("click", () => toggleSegmentPlayback(segment));
  const include = document.createElement("input"); include.type = "checkbox"; include.checked = segment.include;
  const label = document.createElement("input"); label.value = segment.label;
  const kind = document.createElement("input"); kind.value = segment.kind;
  const start = document.createElement("input"); start.value = formatTime(segment.startSeconds, true); start.className = "short"; start.dataset.field = "start";
  const end = document.createElement("input"); end.value = segment.endSeconds == null ? "" : formatTime(segment.endSeconds, true); end.className = "short"; end.dataset.field = "end";
  const save = document.createElement("button"); save.textContent = "Save";
  save.addEventListener("click", async () => {
    save.disabled = true;
    try {
      current = await api(`/api/sessions/${current.id}/segments/${segment.id}`, {method: "PATCH", body: JSON.stringify({include: include.checked, label: label.value, kind: kind.value, startSeconds: parseTime(start.value), endSeconds: parseTime(end.value)})});
      elements.error.textContent = ""; render();
    } catch (error) { showError(error); }
    finally { save.disabled = false; }
  });
  for (const control of [play, include, label, kind, start, end, save]) { const cell = document.createElement("td"); cell.append(control); row.append(cell); }
  return row;
}

async function toggleSegmentPlayback(segment) {
  if (playingSegmentID === segment.id) {
    stopSegmentPlayback();
    return;
  }
  stopSegmentPlayback();
  playingSegmentID = segment.id;
  elements.audio.currentTime = segment.startSeconds;
  renderSegmentRows();
  renderPlayhead();
  try { await elements.audio.play(); }
  catch (error) { playingSegmentID = null; renderSegmentRows(); showError(error); }
}

function stopSegmentPlayback() {
  if (playingSegmentID == null) return;
  playingSegmentID = null;
  elements.audio.pause();
  if (current) renderSegmentRows();
}

function playingSegment() {
  return current?.segments.find(segment => segment.id === playingSegmentID) || null;
}

function markerRow(marker) {
  const row = document.createElement("div"); row.className = "marker";
  const time = document.createElement("button"); time.textContent = formatTime(marker.atSeconds, true); time.addEventListener("click", () => { stopSegmentPlayback(); elements.audio.currentTime = marker.atSeconds; void elements.audio.play(); });
  const label = document.createElement("span"); label.textContent = marker.label;
  const kind = document.createElement("span"); kind.className = "muted"; kind.textContent = marker.kind;
  row.append(time, label, kind); return row;
}

function renderWaveform() {
  if (!current || !elements["waveform-viewport"]) return;
  const loading = waveform.loading || !waveform.peaks;
  elements["waveform-loading"].classList.toggle("hidden", !loading);
  if (waveform.loading) elements["waveform-loading"].textContent = "Generating waveform…";
  const duration = waveform.duration || Math.max(current.durationSeconds || 0, 1);
  if (waveform.viewEnd <= waveform.viewStart) { waveform.viewStart = 0; waveform.viewEnd = duration; }
  elements["waveform-range"].textContent = `${formatTime(waveform.viewStart, true)} – ${formatTime(waveform.viewEnd, true)} · ${Math.max(1, duration/(waveform.viewEnd-waveform.viewStart)).toFixed(1)}×`;
  drawWaveform();
  drawSegments();
  renderPlayhead();
  const full = waveform.viewStart <= 0.001 && waveform.viewEnd >= duration - 0.001;
  elements["zoom-out"].disabled = full;
  elements["zoom-full"].disabled = full;
  elements["pan-left"].disabled = waveform.viewStart <= 0;
  elements["pan-right"].disabled = waveform.viewEnd >= duration;
}

function drawWaveform() {
  const canvas = elements["waveform-canvas"];
  const bounds = elements["waveform-viewport"].getBoundingClientRect();
  const width = Math.max(1, Math.round(bounds.width));
  const height = Math.max(1, Math.round(bounds.height));
  const scale = window.devicePixelRatio || 1;
  if (canvas.width !== Math.round(width*scale) || canvas.height !== Math.round(height*scale)) { canvas.width = Math.round(width*scale); canvas.height = Math.round(height*scale); }
  const ctx = canvas.getContext("2d");
  ctx.setTransform(scale, 0, 0, scale, 0, 0);
  ctx.clearRect(0, 0, width, height);
  ctx.fillStyle = "#0b1015"; ctx.fillRect(0, 0, width, height);
  ctx.strokeStyle = "#26313b"; ctx.beginPath(); ctx.moveTo(0, height/2); ctx.lineTo(width, height/2); ctx.stroke();
  if (!waveform.peaks) return;
  const pps = waveform.pointsPerSecond;
  const first = Math.max(0, Math.floor(waveform.viewStart * pps));
  const last = Math.min(waveform.peaks.length, Math.ceil(waveform.viewEnd * pps));
  const points = Math.max(1, last-first);
  ctx.fillStyle = "#56bd8b";
  for (let x = 0; x < width; x++) {
    const from = first + Math.floor(x*points/width);
    const to = Math.max(from+1, first + Math.ceil((x+1)*points/width));
    let peak = 0;
    for (let index = from; index < Math.min(to, last); index++) peak = Math.max(peak, waveform.peaks[index]);
    const amplitude = Math.max(1, peak/255*(height*.43));
    ctx.fillRect(x, height/2-amplitude, 1, amplitude*2);
  }
}

function drawSegments() {
  const start = waveform.viewStart;
  const end = waveform.viewEnd;
  const span = Math.max(.001, end-start);
  const pieces = [];
  for (const segment of current.segments) {
    if (segment.endSeconds == null || segment.endSeconds < start || segment.startSeconds > end) continue;
    const block = document.createElement("div");
    block.className = `wave-segment${segment.include ? "" : " excluded"}${dragState?.segmentID === segment.id ? " dragging" : ""}`;
    block.dataset.segmentId = segment.id;
    const visibleStart = Math.max(start, segment.startSeconds);
    const visibleEnd = Math.min(end, segment.endSeconds);
    block.style.left = `${100*(visibleStart-start)/span}%`;
    block.style.width = `${Math.max(.15, 100*(visibleEnd-visibleStart)/span)}%`;
    block.title = `${segment.label}: ${formatTime(segment.startSeconds, true)} – ${formatTime(segment.endSeconds, true)}`;
    const label = document.createElement("span"); label.className = "wave-segment-label"; label.textContent = segment.label;
    block.append(label);
    if (segment.startSeconds >= start && segment.startSeconds <= end) block.append(segmentHandle(segment, "start"));
    if (segment.endSeconds >= start && segment.endSeconds <= end) block.append(segmentHandle(segment, "end"));
    block.addEventListener("pointerdown", event => beginDrag(event, segment, "move"));
    pieces.push(block);
  }
  elements["segment-overlay"].replaceChildren(...pieces);
}

function segmentHandle(segment, edge) {
  const handle = document.createElement("button");
  handle.className = `segment-handle ${edge}`;
  handle.type = "button";
  handle.title = `Adjust ${edge} of ${segment.label}`;
  handle.setAttribute("aria-label", handle.title);
  handle.addEventListener("pointerdown", event => beginDrag(event, segment, edge));
  return handle;
}

function beginDrag(event, segment, mode) {
  if (segment.endSeconds == null) return;
  event.preventDefault(); event.stopPropagation();
  dragState = {segmentID: segment.id, mode, pointerX: event.clientX, originalStart: segment.startSeconds, originalEnd: segment.endSeconds};
  window.addEventListener("pointermove", continueDrag);
  window.addEventListener("pointerup", finishDrag, {once: true});
  drawSegments();
}

function continueDrag(event) {
  if (!dragState) return;
  const segment = current.segments.find(item => item.id === dragState.segmentID);
  if (!segment) return;
  const width = Math.max(1, elements["waveform-viewport"].getBoundingClientRect().width);
  const delta = (event.clientX-dragState.pointerX)/width*(waveform.viewEnd-waveform.viewStart);
  const minimum = .1;
  if (dragState.mode === "start") segment.startSeconds = clamp(dragState.originalStart+delta, 0, dragState.originalEnd-minimum);
  if (dragState.mode === "end") segment.endSeconds = clamp(dragState.originalEnd+delta, dragState.originalStart+minimum, waveform.duration);
  if (dragState.mode === "move") {
    const length = dragState.originalEnd-dragState.originalStart;
    const start = clamp(dragState.originalStart+delta, 0, waveform.duration-length);
    segment.startSeconds = start; segment.endSeconds = start+length;
  }
  updateSegmentInputs(segment);
  drawSegments(); renderPlayhead();
}

async function finishDrag() {
  window.removeEventListener("pointermove", continueDrag);
  const state = dragState;
  dragState = null;
  if (!state) return;
  const segment = current.segments.find(item => item.id === state.segmentID);
  drawSegments();
  try {
    current = await api(`/api/sessions/${current.id}/segments/${segment.id}`, {method: "PATCH", body: JSON.stringify({startSeconds: segment.startSeconds, endSeconds: segment.endSeconds})});
    elements.error.textContent = ""; render();
  } catch (error) {
    showError(error);
    current = await api(`/api/sessions/${current.id}`);
    render();
  }
}

function updateSegmentInputs(segment) {
  const row = elements.segments.querySelector(`tr[data-segment-id="${CSS.escape(segment.id)}"]`);
  if (!row) return;
  const start = row.querySelector('[data-field="start"]');
  const end = row.querySelector('[data-field="end"]');
  if (start) start.value = formatTime(segment.startSeconds, true);
  if (end) end.value = formatTime(segment.endSeconds, true);
}

function zoom(factor, anchor = null) {
  const duration = waveform.duration || 1;
  const oldSpan = waveform.viewEnd-waveform.viewStart;
  const newSpan = clamp(oldSpan*factor, 10, duration);
  if (newSpan >= duration) { showFullWaveform(); return; }
  if (anchor == null || anchor < waveform.viewStart || anchor > waveform.viewEnd) anchor = (waveform.viewStart+waveform.viewEnd)/2;
  const ratio = (anchor-waveform.viewStart)/oldSpan;
  let start = anchor-ratio*newSpan;
  start = clamp(start, 0, duration-newSpan);
  waveform.viewStart = start; waveform.viewEnd = start+newSpan; renderWaveform();
}

function pan(fraction) {
  const span = waveform.viewEnd-waveform.viewStart;
  const start = clamp(waveform.viewStart+fraction*span, 0, Math.max(0, waveform.duration-span));
  waveform.viewStart = start; waveform.viewEnd = start+span; renderWaveform();
}

function showFullWaveform() {
  waveform.viewStart = 0; waveform.viewEnd = waveform.duration || Math.max(current?.durationSeconds || 0, 1); renderWaveform();
}

function seekFromPointer(event) {
  if (event.target.closest(".wave-segment")) return;
  const bounds = elements["waveform-viewport"].getBoundingClientRect();
  const ratio = clamp((event.clientX-bounds.left)/bounds.width, 0, 1);
  stopSegmentPlayback();
  elements.audio.currentTime = waveform.viewStart+ratio*(waveform.viewEnd-waveform.viewStart);
  renderPlayhead();
}

function renderPlayhead() {
  const time = elements.audio.currentTime || 0;
  const visible = time >= waveform.viewStart && time <= waveform.viewEnd;
  elements.playhead.classList.toggle("hidden", !visible);
  if (visible) elements.playhead.style.left = `${100*(time-waveform.viewStart)/(waveform.viewEnd-waveform.viewStart)}%`;
}

function clamp(value, minimum, maximum) { return Math.max(minimum, Math.min(maximum, value)); }

elements.audio.addEventListener("timeupdate", () => {
  const segment = playingSegment();
  if (segment?.endSeconds != null && elements.audio.currentTime >= segment.endSeconds) {
    elements.audio.pause(); elements.audio.currentTime = segment.endSeconds; playingSegmentID = null; renderSegmentRows();
  }
  renderPlayhead();
});
elements.audio.addEventListener("pause", () => {
  if (playingSegmentID != null && (!playingSegment() || elements.audio.currentTime < playingSegment().endSeconds-.05)) { playingSegmentID = null; renderSegmentRows(); }
});
elements.audio.addEventListener("ended", stopSegmentPlayback);
elements["waveform-viewport"].addEventListener("click", seekFromPointer);
elements["waveform-viewport"].addEventListener("wheel", event => {
  event.preventDefault();
  const bounds = elements["waveform-viewport"].getBoundingClientRect();
  const anchor = waveform.viewStart+clamp((event.clientX-bounds.left)/bounds.width, 0, 1)*(waveform.viewEnd-waveform.viewStart);
  zoom(event.deltaY < 0 ? .6 : 1.65, anchor);
}, {passive: false});
elements["zoom-in"].addEventListener("click", () => zoom(.5, elements.audio.currentTime));
elements["zoom-out"].addEventListener("click", () => zoom(2, elements.audio.currentTime));
elements["zoom-full"].addEventListener("click", showFullWaveform);
elements["pan-left"].addEventListener("click", () => pan(-.5));
elements["pan-right"].addEventListener("click", () => pan(.5));
window.addEventListener("resize", renderWaveform);

elements["add-marker"].addEventListener("submit", async event => {
  event.preventDefault();
  try {
    current = await api(`/api/sessions/${current.id}/markers`, {method: "POST", body: JSON.stringify({label: elements["marker-label"].value, kind: elements["marker-kind"].value, atSeconds: parseTime(elements["marker-time"].value)})});
    elements["marker-label"].value = ""; elements["marker-kind"].value = ""; render();
  } catch (error) { showError(error); }
});

elements.export.addEventListener("click", async () => {
  try { await api(`/api/sessions/${current.id}/export`, {method: "POST", body: "{}"}); current.export = {status: "running"}; render(); }
  catch (error) { showError(error); }
});

function showError(error) { elements.error.textContent = error.message; }

loadSessions();
setInterval(async () => {
  if (current?.export?.status === "running" || current?.status === "recording") { try { current = await api(`/api/sessions/${current.id}`); render(); } catch (_) {} }
}, 1500);
