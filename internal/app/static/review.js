"use strict";
const {api, formatTime, parseTime} = window.SC;
const elementIDs = [
  "session-list", "empty", "detail", "session-details", "session-title", "church",
  "save-session", "session-meta", "audio", "open-folder",
  "waveform-viewport", "waveform-canvas", "segment-overlay", "playhead",
  "waveform-loading", "waveform-range", "pan-left", "pan-right", "zoom-in",
  "zoom-out", "zoom-full", "segments", "markers", "add-marker", "marker-label",
  "marker-time", "export", "download", "export-status", "error",
  "show-add-segment", "add-segment", "cancel-add-segment", "new-segment-label",
  "new-segment-start", "new-segment-end", "removed-panel",
  "removed-count", "removed-segments"
];
const elements = Object.fromEntries(elementIDs.map(id => [id, document.getElementById(id)]));

let current = null;
let sessions = [];
let playingSegmentID = null;
let waveform = emptyWaveform();
let dragState = null;
let audioSource = null;

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
    if (!isRecording(current)) void loadWaveform(id);
    await loadSessions();
  } catch (error) { showError(error); }
}

function isRecording(session) { return session?.status === "recording" || session?.status === "starting"; }

// The recording grows under a fixed path, so the finished file is a different
// resource at the same URL. Keying the source on the published duration makes
// the player reload the complete recording instead of keeping the partial one
// it fetched while the service was still being recorded.
function audioSourceFor(session) {
  if (!session || isRecording(session)) return null;
  return `/api/sessions/${session.id}/audio?duration=${session.durationSeconds || 0}`;
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
  if (!elements["session-details"].contains(document.activeElement)) {
    elements["session-title"].value = current.title;
    elements.church.value = current.church || "Church";
  }
  const capture = current.capture || {};
  const captureText = capture.sampleRate ? ` · ${capture.sampleRate/1000} kHz · ${capture.droppedFrames || 0} dropped frames` : "";
  elements["session-meta"].textContent = `${new Date(current.startedAt).toLocaleString()} · ${formatTime(current.durationSeconds, true)} · ${current.status}${captureText}`;
  const wanted = audioSourceFor(current);
  if (wanted !== audioSource) {
    audioSource = wanted;
    stopSegmentPlayback();
    if (wanted) elements.audio.src = wanted;
    else elements.audio.removeAttribute("src");
    elements.audio.load();
  }
  elements.audio.classList.toggle("hidden", !wanted);
  renderSegmentRows();
  elements.markers.replaceChildren(...[...current.markers].sort((a,b) => a.atSeconds-b.atSeconds).map(markerRow));
  renderWaveform();
  const exp = current.export;
  elements["export-status"].textContent = exp ? exp.status === "running" ? "Creating MP3…" : exp.status === "failed" ? `Export failed: ${exp.error}` : exp.status === "stale" ? "Service details or segments changed since this MP3 was created. Create a new MP3." : "MP3 ready." : "";
  elements.export.disabled = isRecording(current) || exp?.status === "running";
  elements["save-session"].disabled = exp?.status === "running";
  elements.download.classList.toggle("hidden", exp?.status !== "completed");
  if (exp?.status === "completed") elements.download.href = `/api/sessions/${current.id}/export-file`;
}

function renderSegmentRows() {
  elements.segments.replaceChildren(...activeSegments().sort((a,b) => a.startSeconds-b.startSeconds).map(segmentRow));
  const removed = current.segments.filter(segment => segment.archived).sort((a,b) => a.startSeconds-b.startSeconds);
  elements["removed-count"].textContent = removed.length;
  elements["removed-panel"].classList.toggle("hidden", removed.length === 0);
  elements["removed-segments"].replaceChildren(...removed.map(removedSegmentRow));
}

function activeSegments() { return current?.segments.filter(segment => !segment.archived) || []; }

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
  const start = document.createElement("input"); start.value = formatTime(segment.startSeconds, true); start.className = "short"; start.dataset.field = "start";
  const end = document.createElement("input"); end.value = segment.endSeconds == null ? "" : formatTime(segment.endSeconds, true); end.className = "short"; end.dataset.field = "end";
  const save = document.createElement("button"); save.textContent = "Save";
  save.addEventListener("click", async () => {
    save.disabled = true;
    try {
      current = await api(`/api/sessions/${current.id}/segments/${segment.id}`, {method: "PATCH", body: JSON.stringify({include: include.checked, label: label.value, startSeconds: parseTime(start.value), endSeconds: parseTime(end.value)})});
      elements.error.textContent = ""; render();
    } catch (error) { showError(error); }
    finally { save.disabled = false; }
  });
  const remove = document.createElement("button"); remove.textContent = "Remove"; remove.className = "remove";
  remove.addEventListener("click", () => removeSegment(segment));
  const actions = document.createElement("div"); actions.className = "row-actions"; actions.append(save, remove);
  for (const control of [play, include, label, start, end, actions]) { const cell = document.createElement("td"); cell.append(control); row.append(cell); }
  return row;
}

function removedSegmentRow(segment) {
  const row = document.createElement("div"); row.className = "removed-segment";
  const label = document.createElement("strong"); label.textContent = segment.label;
  const times = document.createElement("span"); times.className = "muted time"; times.textContent = `${formatTime(segment.startSeconds, true)} – ${formatTime(segment.endSeconds, true)}`;
  const restore = document.createElement("button"); restore.textContent = "Restore"; restore.addEventListener("click", () => restoreSegment(segment));
  row.append(label, times, restore); return row;
}

async function removeSegment(segment) {
  if (playingSegmentID === segment.id) stopSegmentPlayback();
  try {
    current = await api(`/api/sessions/${current.id}/segments/${segment.id}`, {method: "DELETE"});
    elements.error.textContent = ""; render();
  } catch (error) { showError(error); }
}

async function restoreSegment(segment) {
  try {
    current = await api(`/api/sessions/${current.id}/segments/${segment.id}/restore`, {method: "POST", body: "{}"});
    elements.error.textContent = ""; render();
  } catch (error) { showError(error); }
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
  return activeSegments().find(segment => segment.id === playingSegmentID) || null;
}

function markerRow(marker) {
  const row = document.createElement("div"); row.className = "marker";
  const time = document.createElement("button"); time.textContent = formatTime(marker.atSeconds, true); time.addEventListener("click", () => { stopSegmentPlayback(); elements.audio.currentTime = marker.atSeconds; void elements.audio.play(); });
  const label = document.createElement("span"); label.textContent = marker.label;
  row.append(time, label); return row;
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
  // Each column covers the peaks for the time it represents, so a recorded
  // duration longer than the decoded audio leaves empty space at the end
  // instead of stretching the waveform away from the segments and playhead.
  const pps = waveform.pointsPerSecond;
  const span = Math.max(.001, waveform.viewEnd-waveform.viewStart);
  ctx.fillStyle = "#56bd8b";
  for (let x = 0; x < width; x++) {
    const from = Math.max(0, Math.floor((waveform.viewStart+x*span/width)*pps));
    const to = Math.min(waveform.peaks.length, Math.max(from+1, Math.ceil((waveform.viewStart+(x+1)*span/width)*pps)));
    if (from >= waveform.peaks.length) break;
    let peak = 0;
    for (let index = from; index < to; index++) peak = Math.max(peak, waveform.peaks[index]);
    const amplitude = Math.max(1, peak/255*(height*.43));
    ctx.fillRect(x, height/2-amplitude, 1, amplitude*2);
  }
}

function drawSegments() {
  const start = waveform.viewStart;
  const end = waveform.viewEnd;
  const span = Math.max(.001, end-start);
  const pieces = [];
  for (const segment of activeSegments()) {
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
  const neighbours = segmentNeighbours(segment);
  dragState = {segmentID: segment.id, mode, pointerX: event.clientX, originalStart: segment.startSeconds, originalEnd: segment.endSeconds, minimumStart: neighbours.previous?.endSeconds ?? 0, maximumEnd: neighbours.next?.startSeconds ?? waveform.duration};
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
  if (dragState.mode === "start") segment.startSeconds = clamp(dragState.originalStart+delta, dragState.minimumStart, dragState.originalEnd-minimum);
  if (dragState.mode === "end") segment.endSeconds = clamp(dragState.originalEnd+delta, dragState.originalStart+minimum, dragState.maximumEnd);
  if (dragState.mode === "move") {
    const length = dragState.originalEnd-dragState.originalStart;
    const start = clamp(dragState.originalStart+delta, dragState.minimumStart, dragState.maximumEnd-length);
    segment.startSeconds = start; segment.endSeconds = start+length;
  }
  updateSegmentInputs(segment);
  drawSegments(); renderPlayhead();
}

function segmentNeighbours(segment) {
  const ordered = activeSegments().filter(item => item.endSeconds != null).sort((a,b) => a.startSeconds-b.startSeconds);
  const index = ordered.findIndex(item => item.id === segment.id);
  return {previous: index > 0 ? ordered[index-1] : null, next: index >= 0 && index < ordered.length-1 ? ordered[index+1] : null};
}

async function finishDrag() {
  window.removeEventListener("pointermove", continueDrag);
  const state = dragState;
  dragState = null;
  if (!state) return;
  const segment = current.segments.find(item => item.id === state.segmentID);
  drawSegments();
  if (!segment) return;
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
    current = await api(`/api/sessions/${current.id}/markers`, {method: "POST", body: JSON.stringify({label: elements["marker-label"].value, atSeconds: parseTime(elements["marker-time"].value)})});
    elements["marker-label"].value = ""; render();
  } catch (error) { showError(error); }
});

elements["session-details"].addEventListener("submit", async event => {
  event.preventDefault();
  try { await saveSessionDetails(); }
  catch (error) { showError(error); }
});

async function saveSessionDetails() {
  const title = elements["session-title"].value.trim();
  const church = elements.church.value.trim();
  if (title === current.title && church === current.church) return;
  elements["save-session"].disabled = true;
  try {
    current = await api(`/api/sessions/${current.id}`, {method: "PATCH", body: JSON.stringify({title, church})});
    elements.error.textContent = ""; render(); await loadSessions();
  } finally { elements["save-session"].disabled = false; }
}

elements["show-add-segment"].addEventListener("click", () => {
  const duration = Math.max(current?.durationSeconds || waveform.duration || 0, .1);
  let start = clamp(elements.audio.currentTime || 0, 0, Math.max(0, duration-.1));
  if (start >= duration-.1) start = Math.max(0, duration-60);
  const end = Math.min(duration, start+60);
  elements["new-segment-label"].value = "";
  elements["new-segment-start"].value = formatTime(start, true);
  elements["new-segment-end"].value = formatTime(Math.max(start+.1, end), true);
  elements["add-segment"].classList.remove("hidden");
  elements["new-segment-label"].focus();
});

elements["cancel-add-segment"].addEventListener("click", () => elements["add-segment"].classList.add("hidden"));

elements["add-segment"].addEventListener("submit", async event => {
  event.preventDefault();
  try {
    current = await api(`/api/sessions/${current.id}/segments/manual`, {method: "POST", body: JSON.stringify({
      label: elements["new-segment-label"].value,
      startSeconds: parseTime(elements["new-segment-start"].value),
      endSeconds: parseTime(elements["new-segment-end"].value)
    })});
    elements["add-segment"].classList.add("hidden");
    elements.error.textContent = ""; render();
  } catch (error) { showError(error); }
});

elements.export.addEventListener("click", async () => {
  try { await saveSessionDetails(); await api(`/api/sessions/${current.id}/export`, {method: "POST", body: "{}"}); current.export = {status: "running"}; render(); }
  catch (error) { showError(error); }
});

elements["open-folder"].addEventListener("click", async () => {
  try { await api(`/api/sessions/${current.id}/open-export-folder`, {method: "POST", body: "{}"}); }
  catch (error) { showError(error); }
});

function showError(error) { elements.error.textContent = error.message; }

loadSessions();
setInterval(async () => {
  if (!current || (current.export?.status !== "running" && !isRecording(current))) return;
  const wasRecording = isRecording(current);
  try {
    current = await api(`/api/sessions/${current.id}`);
    render();
    if (wasRecording && !isRecording(current)) { void loadWaveform(current.id); void loadSessions(); }
  } catch (_) {}
}, 1500);
