"use strict";
window.SC = {
  async api(path, options = {}) {
    const response = await fetch(path, {...options, headers: {"Content-Type": "application/json", ...(options.headers || {})}});
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(body.error || `Request failed (${response.status})`);
      error.status = response.status;
      throw error;
    }
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
    const fields = String(value).trim().split(":");
    const parts = fields.map(Number);
    if (fields.length > 3 || fields.some(field => field.trim() === "") || parts.some(v => !Number.isFinite(v) || v < 0)) throw new Error("Use a time such as 12:34.5");
    let result = 0;
    for (const part of parts) result = result * 60 + part;
    return result;
  },
  // Suggest the first usable interval at or after the cursor. Complete,
  // non-archived segments reserve their intervals whether or not they are
  // included in the MP3, matching the backend overlap rule.
  suggestSegmentRange(cursor, duration, segments) {
    const minimum = 0.1;
    const preferred = 60;
    duration = Math.max(Number(duration) || 0, minimum);
    let start = Math.max(0, Math.min(Number(cursor) || 0, duration));
    if (start >= duration-minimum) start = Math.max(0, duration-preferred);
    const ordered = (segments || [])
      .filter(segment => !segment.archived && Number.isFinite(segment.startSeconds) && Number.isFinite(segment.endSeconds) && segment.endSeconds > segment.startSeconds)
      .sort((a, b) => a.startSeconds-b.startSeconds);
    for (const segment of ordered) {
      if (segment.endSeconds <= start) continue;
      if (segment.startSeconds > start) {
        if (segment.startSeconds-start >= minimum) {
          return {start, end: Math.min(start+preferred, segment.startSeconds)};
        }
      }
      start = Math.max(start, segment.endSeconds);
    }
    if (duration-start < minimum) return null;
    return {start, end: Math.min(duration, start+preferred)};
  }
};
