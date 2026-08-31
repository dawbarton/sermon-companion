"use strict";
const {api, formatTime} = window.SC;
const elements = Object.fromEntries(["status-dot", "timer", "start-panel", "record-panel", "title", "start", "presets", "marker", "stop", "capture-health", "open-review", "error"].map(id => [id, document.getElementById(id)]));
let status = {active: false, presets: []};
// A failed action, such as starting a recording against a missing microphone,
// stays on screen until the operator tries something else. The status poll
// runs every second and must not wipe it.
let actionError = "";
let pollError = "";

function showError(error) { actionError = error instanceof Error ? error.message : error; renderError(); }
function clearError() { actionError = ""; renderError(); }
function renderError() { elements.error.textContent = actionError || pollError; }

function render() {
  elements["start-panel"].classList.toggle("hidden", status.active);
  elements["record-panel"].classList.toggle("hidden", !status.active);
  elements["status-dot"].classList.toggle("live", status.active);
  elements.timer.textContent = formatTime(status.elapsedSeconds || 0);
  if (!status.active) return;
  // The dock is a narrow panel inside OBS, so it says nothing while the
  // recording is healthy: the running timer is the sign of that. Only a fault
  // takes up a line.
  const dropped = status.capture?.droppedFrames || 0;
  elements["capture-health"].textContent = dropped ? `Recording problem: ${dropped} audio frames were lost.` : "";
  elements["capture-health"].classList.toggle("hidden", !dropped);
  const open = status.session?.segments?.find(segment => !segment.archived && segment.endSeconds == null);
  elements.presets.replaceChildren(...status.presets.map(preset => {
    const button = document.createElement("button");
    const isOpen = open?.kind === preset.kind;
    button.className = `preset${isOpen ? " active" : ""}`;
    // One line per button, and nothing under it: the dock shares a narrow OBS
    // panel with other docks, and the label already says what pressing it does.
    button.textContent = isOpen ? `End ${preset.label}` : `Start ${preset.label}`;
    button.addEventListener("click", () => isOpen ? stopSegment(open.id) : startSegment(preset));
    return button;
  }));
}

// Says on a button that its action happened, then puts the button back.
function flash(button, text, milliseconds = 1800) {
  const original = button.dataset.label || button.textContent;
  button.dataset.label = original;
  button.textContent = text;
  clearTimeout(Number(button.dataset.flash));
  button.dataset.flash = String(setTimeout(() => { button.textContent = original; }, milliseconds));
}

async function refresh() {
  try { status = await api("/api/status"); pollError = ""; renderError(); render(); }
  catch (error) { pollError = error.message; renderError(); }
}

elements.start.addEventListener("click", async () => {
  elements.start.disabled = true;
  clearError();
  try { await api("/api/sessions", {method: "POST", body: JSON.stringify({title: elements.title.value})}); await refresh(); }
  catch (error) { showError(error); }
  finally { elements.start.disabled = false; }
});

elements["open-review"].addEventListener("click", async () => {
  clearError();
  try { await api("/api/open-review-page", {method: "POST", body: "{}"}); }
  catch (error) { showError(error); }
});

async function startSegment(preset) {
  try {
    clearError();
    status.session = await api(`/api/sessions/${status.session.id}/segments`, {method: "POST", body: JSON.stringify(preset)});
    render();
  } catch (error) { showError(error); }
}

async function stopSegment(id) {
  try {
    clearError();
    status.session = await api(`/api/sessions/${status.session.id}/segments/${id}/stop`, {method: "POST", body: "{}"});
    render();
  } catch (error) { showError(error); }
}

elements.marker.addEventListener("click", async () => {
  try {
    clearError();
    status.session = await api(`/api/sessions/${status.session.id}/markers`, {method: "POST", body: JSON.stringify({kind: "note", label: "Operator marker"})});
    // Confirmation goes on the button itself, so acknowledging a marker costs
    // the dock no height.
    flash(elements.marker, `Marked ${formatTime(status.elapsedSeconds)}`);
  } catch (error) { showError(error); }
});

elements.stop.addEventListener("click", async () => {
  if (!confirm("Stop the continuous recording? You can adjust all markers afterwards.")) return;
  elements.stop.disabled = true;
  clearError();
  try { await api(`/api/sessions/${status.session.id}/stop`, {method: "POST", body: "{}"}); await refresh(); }
  catch (error) { showError(error); }
  finally { elements.stop.disabled = false; }
});

refresh();
setInterval(refresh, 1000);
