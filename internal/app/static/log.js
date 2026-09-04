"use strict";
const {api} = window.SC;
const elements = Object.fromEntries(["log", "log-path", "open-folder", "error"].map(id => [id, document.getElementById(id)]));

async function refresh() {
  try {
    const state = await api("/api/log");
    elements.error.textContent = "";
    elements["log-path"].textContent = state.path
      ? `Written to ${state.path}. The most recent messages are shown; the file holds more.`
      : "Messages are kept only until this application closes, because the log file could not be opened.";
    // Follow the end of the log only while the reader is already there, so
    // scrolling back to something does not snatch it away a moment later.
    const atEnd = elements.log.scrollHeight - elements.log.scrollTop - elements.log.clientHeight < 24;
    elements.log.textContent = state.lines.length ? state.lines.join("\n") : "Nothing has been logged yet.";
    if (atEnd) elements.log.scrollTop = elements.log.scrollHeight;
  } catch (error) {
    elements.error.textContent = error.message;
  }
}

elements["open-folder"].addEventListener("click", async () => {
  try { elements.error.textContent = ""; await api("/api/open-log-folder", {method: "POST", body: "{}"}); }
  catch (error) { elements.error.textContent = error.message; }
});

refresh();
setInterval(refresh, 3000);
