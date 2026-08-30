"use strict";
const {api, formatTime} = window.SC;
const elements = Object.fromEntries(["status-dot", "timer", "start-panel", "record-panel", "title", "start", "presets", "marker", "stop", "last", "error"].map(id => [id, document.getElementById(id)]));
let status = {active: false, presets: []};

function render() {
  elements["start-panel"].classList.toggle("hidden", status.active);
  elements["record-panel"].classList.toggle("hidden", !status.active);
  elements["status-dot"].classList.toggle("live", status.active);
  elements.timer.textContent = formatTime(status.elapsedSeconds || 0);
  if (!status.active) return;
  const open = status.session?.segments?.find(segment => !segment.archived && segment.endSeconds == null);
  elements.presets.replaceChildren(...status.presets.map(preset => {
    const button = document.createElement("button");
    const isOpen = open?.kind === preset.kind;
    button.className = `preset${isOpen ? " active" : ""}`;
    const label = document.createElement("span");
    label.textContent = isOpen ? `End ${preset.label}` : `Start ${preset.label}`;
    const note = document.createElement("small");
    note.textContent = isOpen ? `Started at ${formatTime(open.startSeconds, true)}` : open ? `This will end ${open.label}` : "Mark this part of the service";
    button.append(label, note);
    button.addEventListener("click", () => isOpen ? stopSegment(open.id) : startSegment(preset));
    return button;
  }));
}

async function refresh() {
  try { status = await api("/api/status"); elements.error.textContent = ""; render(); }
  catch (error) { elements.error.textContent = error.message; }
}

elements.start.addEventListener("click", async () => {
  elements.start.disabled = true;
  try { await api("/api/sessions", {method: "POST", body: JSON.stringify({title: elements.title.value})}); await refresh(); }
  catch (error) { elements.error.textContent = error.message; }
  finally { elements.start.disabled = false; }
});

async function startSegment(preset) {
  try {
    status.session = await api(`/api/sessions/${status.session.id}/segments`, {method: "POST", body: JSON.stringify(preset)});
    elements.last.textContent = `${preset.label} started. The complete audio is still being kept.`;
    render();
  } catch (error) { elements.error.textContent = error.message; }
}

async function stopSegment(id) {
  try {
    status.session = await api(`/api/sessions/${status.session.id}/segments/${id}/stop`, {method: "POST", body: "{}"});
    elements.last.textContent = "Segment ended. The complete audio is still being kept.";
    render();
  } catch (error) { elements.error.textContent = error.message; }
}

elements.marker.addEventListener("click", async () => {
  try {
    status.session = await api(`/api/sessions/${status.session.id}/markers`, {method: "POST", body: JSON.stringify({kind: "note", label: "Operator marker"})});
    elements.last.textContent = `Marker added at ${formatTime(status.elapsedSeconds, true)}.`;
  } catch (error) { elements.error.textContent = error.message; }
});

elements.stop.addEventListener("click", async () => {
  if (!confirm("Stop the continuous recording? You can adjust all markers afterwards.")) return;
  elements.stop.disabled = true;
  try { await api(`/api/sessions/${status.session.id}/stop`, {method: "POST", body: "{}"}); await refresh(); }
  catch (error) { elements.error.textContent = error.message; }
  finally { elements.stop.disabled = false; }
});

refresh();
setInterval(refresh, 1000);
