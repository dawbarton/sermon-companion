"use strict";
window.SC = {
  async api(path, options = {}) {
    const response = await fetch(path, {headers: {"Content-Type": "application/json"}, ...options});
    const body = await response.json().catch(() => ({}));
    if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
    return body;
  },
  formatTime(seconds, tenths = false) {
    seconds = Math.max(0, Number(seconds) || 0);
    // Round before splitting, so 119.98 s reads as 2:00.0 rather than 1:60.0.
    const total = tenths ? Math.round(seconds * 10) / 10 : Math.floor(seconds);
    const hours = Math.floor(total / 3600);
    const minutes = Math.floor((total % 3600) / 60);
    const secs = total % 60;
    const suffix = tenths ? secs.toFixed(1).padStart(4, "0") : secs.toString().padStart(2, "0");
    return hours ? `${hours}:${minutes.toString().padStart(2, "0")}:${suffix}` : `${minutes}:${suffix}`;
  },
  parseTime(value) {
    const parts = String(value).trim().split(":").map(Number);
    if (!parts.length || parts.some(v => !Number.isFinite(v) || v < 0)) throw new Error("Use a time such as 12:34.5");
    let result = 0;
    for (const part of parts) result = result * 60 + part;
    return result;
  }
};

