"use strict";
const {api, formatTime} = window.SC;
const elements = Object.fromEntries(["record-header", "status-dot", "timer", "start-panel", "record-panel", "device", "start", "presets", "marker", "stop", "capture-health", "open-review", "error"].map(id => [id, document.getElementById(id)]));
let status = {active: false, presets: []};
let devices = null;
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
  // Nothing above the controls while idle: the dock shares a narrow OBS panel,
  // and a stopped clock reading zero is not worth a line of it.
  elements["record-header"].classList.toggle("hidden", !status.active);
  elements["status-dot"].classList.toggle("live", status.active);
  elements.timer.textContent = formatTime(status.elapsedSeconds || 0);
  if (!status.active) return;
  // The dock says nothing while the recording is healthy: the running timer is
  // the sign of that. Only a fault takes up a line.
  const dropped = status.capture?.droppedFrames || 0;
  elements["capture-health"].textContent = dropped ? `Recording problem: ${dropped} audio frames were lost.` : "";
  elements["capture-health"].classList.toggle("hidden", !dropped);
  const open = status.session?.segments?.find(segment => !segment.archived && segment.endSeconds == null);
  elements.presets.replaceChildren(...status.presets.map(preset => {
    const button = document.createElement("button");
    const isOpen = open?.kind === preset.kind;
    button.className = `preset${isOpen ? " active" : ""}`;
    // One line per button, and nothing under it: the label already says what
    // pressing it does.
    button.textContent = isOpen ? `End ${preset.label}` : `Start ${preset.label}`;
    button.addEventListener("click", () => isOpen ? stopSegment(open.id) : startSegment(preset));
    return button;
  }));
}

function option(value, text, disabled = false) {
  const element = document.createElement("option");
  element.value = value;
  element.textContent = text;
  element.disabled = disabled;
  return element;
}

function renderDevices() {
  const select = elements.device;
  if (!devices) {
    select.replaceChildren(option("", "Finding audio devices…", true));
    select.disabled = true;
    return;
  }
  if (!devices.selectable) {
    // The FFmpeg backends describe their devices as prose meant for a person,
    // so there is nothing honest to put in a list. Show what is configured.
    select.replaceChildren(option("", `${devices.selectedName || "Configured device"} (set in config.json)`, true));
    select.disabled = true;
    return;
  }
  const choices = [option("", "System default input")];
  if (devices.selectedMissing) {
    // The saved device is gone. Say so here rather than letting the operator
    // discover it when the recording refuses to start. The warning leads,
    // because a narrow OBS dock truncates the end of a long device name.
    choices.push(option("__missing__", `⚠ Not connected: ${devices.selectedName || "saved device"}`, true));
  }
  for (const device of devices.devices) {
    choices.push(option(device.id, device.isDefault ? `${device.name} (default)` : device.name));
  }
  select.replaceChildren(...choices);
  select.value = devices.selectedMissing ? "__missing__" : (devices.selectedId || "");
  select.disabled = false;
}

async function refreshDevices() {
  try { devices = await api("/api/devices"); }
  catch (error) { devices = {selectable: false, devices: [], selectedName: "", error: error.message}; }
  renderDevices();
}

elements.device.addEventListener("change", async () => {
  clearError();
  const chosen = elements.device.value;
  elements.device.disabled = true;
  try { await api("/api/devices", {method: "POST", body: JSON.stringify({id: chosen})}); }
  catch (error) { showError(error); }
  finally { await refreshDevices(); }
});

// Says on a button that its action happened, then puts the button back.
function flash(button, text, milliseconds = 1800) {
  const original = button.dataset.label || button.textContent;
  button.dataset.label = original;
  button.textContent = text;
  clearTimeout(Number(button.dataset.flash));
  button.dataset.flash = String(setTimeout(() => { button.textContent = original; }, milliseconds));
}

async function refresh() {
  const wasActive = status.active;
  try { status = await api("/api/status"); pollError = ""; renderError(); render(); }
  catch (error) { pollError = error.message; renderError(); return; }
  // A device can be plugged in or taken away while a service is recorded, so
  // the list is read again once the dock returns to its idle state.
  if (wasActive && !status.active) refreshDevices();
}

elements.start.addEventListener("click", async () => {
  elements.start.disabled = true;
  clearError();
  // No title is asked for: a service is named after its date unless the review
  // page is used to change it.
  try { await api("/api/sessions", {method: "POST", body: "{}"}); await refresh(); }
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

// OBS keeps a hidden dock loaded, so the list is read again when the panel is
// next looked at rather than only when OBS started.
document.addEventListener("visibilitychange", () => { if (!document.hidden && !status.active) refreshDevices(); });

renderDevices();
refreshDevices();
refresh();
setInterval(refresh, 1000);
