"use strict";
// The upload page: the talk's details for the church website, each saving
// itself as it is changed, beside what the website will make of them, and the
// Publish button. The details were filled in from the preaching programme, the
// website, and YouTube when the page was first opened for this service.
const {api} = window.SC;
const ids = [
  "back", "mp3-status", "source", "lists-status", "talk", "title", "date", "church",
  "speaker-rows", "add-speaker", "series", "series-check", "passage", "books",
  "image", "image-note", "video", "video-note", "podcast", "speaker-names",
  "series-names", "problems", "warnings", "publish", "refill", "refresh",
  "publish-status", "error"
];
const el = Object.fromEntries(ids.map(id => [id, document.getElementById(id)]));
const sessionID = new URLSearchParams(location.search).get("session");

let session = null;
let check = null;
// A speaker row added with Add another speaker but not yet typed into exists
// only on the page until it has a name.
let pendingSpeaker = false;

function talk() { return session?.talk || {}; }
function exporting() { return session?.export?.status === "running"; }
function publishing() { return session?.publish?.status === "running"; }
function published() { return !!session?.publish?.progress?.finished; }
function locked() { return publishing() || published(); }

async function load() {
  if (!sessionID) { showError(new Error("No service was chosen. Go back to the recordings and press Upload MP3.")); return; }
  el.back.href = `/?session=${encodeURIComponent(sessionID)}`;
  // Filling the details in the first time reads the programme, the website,
  // and YouTube, which can take some seconds.
  el["lists-status"].textContent = "Reading the programme and the website…";
  try {
    session = await api(`/api/sessions/${sessionID}`);
    if (!session.talk && !published()) session = await prefill(false);
    render();
    await refreshCheck(false);
  } catch (error) { showError(error); }
}

async function prefill(replace) {
  return api(`/api/sessions/${sessionID}/talk/prefill`, {method: "POST", body: JSON.stringify({replace}), headers: {"If-Match": `"${session.revision}"`}});
}

async function refreshCheck(refresh) {
  try {
    check = await api(`/api/sessions/${sessionID}/talk/check${refresh ? "?refresh=1" : ""}`);
    renderCheck();
  } catch (error) { showError(error); }
}

// saveTalk sends one change. A rejected change puts back what is saved, which
// is what would be published.
async function saveTalk(change) {
  try {
    session = await api(`/api/sessions/${sessionID}/talk`, {method: "PATCH", body: JSON.stringify(change), headers: {"If-Match": `"${session.revision}"`}});
    el.error.textContent = "";
  } catch (error) {
    showError(error);
    try { session = await api(`/api/sessions/${sessionID}`); } catch (_) {}
  }
  render(true);
  await refreshCheck(false);
}

function setValue(input, value, force) {
  if (force || document.activeElement !== input) input.value = value ?? "";
}

function render(force) {
  if (!session) return;
  const t = talk();
  setValue(el.title, t.title, force);
  setValue(el.date, t.date, force);
  setValue(el.series, t.series, force);
  setValue(el.passage, t.passage, force);
  setValue(el.image, t.image, force);
  setValue(el.video, t.videoUrl, force);
  el.podcast.checked = !!t.podcast;
  renderChurches();
  renderSpeakers();
  el.source.textContent = t.source || "";
  for (const control of el.talk.querySelectorAll("input, select, button")) control.disabled = locked();
  el.refill.disabled = locked();
  renderMP3();
  renderPublish();
}

function renderChurches() {
  const names = [...(check?.churches || [])];
  const chosen = talk().church || "";
  if (chosen && !names.includes(chosen)) names.unshift(chosen);
  el.church.replaceChildren(...names.map(name => new Option(name, name, false, name === chosen)));
}

function renderSpeakers() {
  const names = [...(talk().speakers || [])];
  if (pendingSpeaker || names.length === 0) names.push("");
  const focused = document.activeElement?.dataset?.index;
  el["speaker-rows"].replaceChildren(...names.map((name, index) => speakerRow(name, index)));
  if (focused !== undefined) el["speaker-rows"].querySelector(`input[data-index="${focused}"]`)?.focus();
}

function speakerRow(name, index) {
  const row = document.createElement("div");
  const line = document.createElement("div");
  line.className = "speaker-row";
  const input = document.createElement("input");
  input.value = name;
  input.dataset.index = index;
  input.setAttribute("list", "speaker-names");
  input.setAttribute("aria-label", `Speaker ${index + 1}`);
  input.placeholder = "Start typing a name";
  input.addEventListener("change", () => {
    pendingSpeaker = false;
    const speakers = [...el["speaker-rows"].querySelectorAll("input[data-index]")].map(field => field.value.trim()).filter(Boolean);
    saveTalk({speakers});
  });
  const remove = document.createElement("button");
  remove.type = "button";
  remove.className = "remove-speaker";
  remove.textContent = "✕";
  remove.title = name ? `Remove ${name}` : "Remove";
  remove.addEventListener("click", () => {
    pendingSpeaker = false;
    saveTalk({speakers: (talk().speakers || []).filter((_, i) => i !== index)});
  });
  line.append(input, remove);
  row.append(line, nameCheck(check?.review?.speakers?.find(c => c.name.toLowerCase() === name.toLowerCase()), "speaker", isNew => {
    const confirmed = new Set((talk().newSpeakers || []).map(n => n.toLowerCase()));
    if (isNew) confirmed.add(name.toLowerCase()); else confirmed.delete(name.toLowerCase());
    saveTalk({newSpeakers: (talk().speakers || []).filter(n => confirmed.has(n.toLowerCase()))});
  }, suggestion => {
    const speakers = [...(talk().speakers || [])];
    speakers[index] = suggestion;
    saveTalk({speakers});
  }));
  return row;
}

// nameCheck says what the website makes of a name it does not have exactly:
// the near names it might be a slip for, and a box to confirm it is new.
function nameCheck(result, what, onNew, onSuggestion) {
  const box = document.createElement("div");
  box.className = "name-check";
  if (!result || result.known) return box;
  box.append(result.new ? `New to the website: it will be added when the talk is published.` : `Not on the website yet.`);
  for (const near of result.near || []) {
    const use = document.createElement("button");
    use.type = "button";
    use.className = "suggestion";
    use.textContent = `Use ${near}`;
    use.disabled = locked();
    use.addEventListener("click", () => onSuggestion(near));
    box.append(" ", use);
  }
  const label = document.createElement("label");
  const tick = document.createElement("input");
  tick.type = "checkbox";
  tick.checked = !!result.new;
  tick.disabled = locked();
  tick.addEventListener("change", () => onNew(tick.checked));
  label.append(tick, `This is a new ${what}`);
  box.append(" ", label);
  return box;
}

function renderMP3() {
  const exp = session.export;
  let text = "", state = "";
  if (!exp) text = "No MP3 has been created. Go back to the recording and press Upload MP3.";
  else if (exp.status === "running") { text = "Creating the MP3… This can take a few minutes. The details can be filled in meanwhile."; state = "running"; }
  else if (exp.status === "completed") text = "The MP3 is ready.";
  else if (exp.status === "stale") { text = "The MP3 is out of date because the recording was edited after it was made. Go back to the recording and press Upload MP3 again."; state = "failed"; }
  else if (exp.status === "failed") { text = `Creating the MP3 failed: ${exp.error}`; state = "failed"; }
  el["mp3-status"].textContent = text;
  el["mp3-status"].className = `export-status ${state}`;
}

function renderPublish() {
  const status = el["publish-status"];
  const p = session.publish;
  status.className = "export-status";
  status.replaceChildren();
  if (published()) {
    status.classList.add("done");
    const link = document.createElement("a");
    link.href = p.progress.link;
    link.target = "_blank";
    link.rel = "noopener";
    link.textContent = p.progress.link;
    status.append("Published on the website: ", link);
    if (session.export?.endedAt && p.mp3Finished && session.export.endedAt !== p.mp3Finished) {
      status.append(" The MP3 has been made again since; the website still has the earlier one.");
    }
  } else if (publishing()) {
    status.classList.add("running");
    status.textContent = "Uploading… This can take a few minutes. The upload carries on if this page is closed.";
  } else if (p?.status === "failed") {
    status.classList.add("failed");
    status.textContent = `The upload stopped: ${p.error}`;
  }
  const problems = check?.review?.problems || ["The website's lists have not been read yet."];
  const ready = session.export?.status === "completed" && problems.length === 0 && !locked();
  el.publish.disabled = !ready;
  el.publish.textContent = publishing() ? "Uploading…" : p?.status === "failed" ? "Publish again" : "Publish";
  el.publish.classList.toggle("hidden", published());
}

function renderCheck() {
  if (!check) return;
  el["speaker-names"].replaceChildren(...(check.speakers || []).map(name => new Option(name)));
  el["series-names"].replaceChildren(...(check.series || []).map(name => new Option(name)));
  const fetched = check.listsFetched ? new Date(check.listsFetched).toLocaleString([], {dateStyle: "medium", timeStyle: "short"}) : null;
  el["lists-status"].textContent = check.listsError || (fetched ? `The website's lists were read ${fetched}.` : "");

  const review = check.review;
  el["series-check"].replaceChildren(nameCheck(review?.series, "series",
    isNew => saveTalk({newSeries: isNew}),
    suggestion => saveTalk({series: suggestion})));
  el.books.textContent = review?.books?.length ? `Filed under ${review.books.join(", ")}.` : "";

  el["image-note"].className = "field-note muted";
  el["image-note"].replaceChildren();
  if (check.image?.error) {
    el["image-note"].className = "field-note bad";
    el["image-note"].textContent = check.image.error;
  } else if (check.image?.id) {
    const link = document.createElement("a");
    link.href = check.image.source || check.image.link;
    link.target = "_blank";
    link.rel = "noopener";
    link.textContent = check.image.title || `Image ${check.image.id}`;
    el["image-note"].append(check.image.fromSeries ? "The series' image: " : "Image: ", link);
  } else if (!locked()) {
    el["image-note"].textContent = "The talk will have no image.";
  }
  el["video-note"].textContent = check.video ? `YouTube: “${check.video}”` : talk().videoUrl ? "" : "No video.";

  const problems = review ? [...review.problems] : [];
  const warnings = review ? [...review.warnings] : [];
  for (const duplicate of check.duplicates || []) {
    warnings.push(`The website already has a talk for this church and day: “${duplicate.title}”. Check it is not this one before publishing.`);
  }
  for (const problem of check.programmeProblems || []) warnings.push(`In the programme: ${problem}`);
  warnings.push(...(check.notes || []));
  if (check.status !== "publish") warnings.push(`The settings make this upload ${check.status === "private" ? "private, visible only when signed in to the website" : "a draft"}.`);
  el.problems.replaceChildren(...(locked() ? [] : problems).map(text => listItem(text)));
  el.warnings.replaceChildren(...(locked() ? [] : warnings).map(text => listItem(text)));
  renderChurches();
  renderSpeakers();
  renderPublish();
}

function listItem(text) {
  const item = document.createElement("li");
  item.textContent = text;
  return item;
}

function showError(error) { el.error.textContent = error.message; }

// Each field saves itself when it is left, like the review page.
for (const [id, field] of [["title", "title"], ["date", "date"], ["series", "series"], ["passage", "passage"], ["image", "image"], ["video", "videoUrl"]]) {
  el[id].addEventListener("change", () => saveTalk({[field]: el[id].value}));
  el[id].addEventListener("keydown", event => { if (event.key === "Enter") { event.preventDefault(); el[id].blur(); } });
}
el.church.addEventListener("change", () => saveTalk({church: el.church.value}));
el.podcast.addEventListener("change", () => saveTalk({podcast: el.podcast.checked}));
el.talk.addEventListener("submit", event => event.preventDefault());
el["add-speaker"].addEventListener("click", () => {
  pendingSpeaker = true;
  renderSpeakers();
  const inputs = el["speaker-rows"].querySelectorAll("input[data-index]");
  inputs[inputs.length - 1]?.focus();
});

el.refill.addEventListener("click", async () => {
  if (!confirm("Replace the details on this page with the ones from the programme, the website, and YouTube?")) return;
  try { session = await prefill(true); el.error.textContent = ""; } catch (error) { showError(error); }
  render(true);
  await refreshCheck(false);
});

el.refresh.addEventListener("click", () => refreshCheck(true));

el.publish.addEventListener("click", async () => {
  const t = talk();
  let allowDuplicate = false;
  if (check?.duplicates?.length) {
    const titles = check.duplicates.map(d => `“${d.title}”`).join(", ");
    if (!confirm(`The website already has ${titles} for this church and day. Publish this talk as well?`)) return;
    allowDuplicate = true;
  }
  const visible = check?.status === "publish" ? "Everyone will be able to see it." : "";
  if (!confirm(`Publish “${t.title}” on the website now? ${visible}`)) return;
  try {
    session = await api(`/api/sessions/${sessionID}/publish`, {method: "POST", body: JSON.stringify({allowDuplicate}), headers: {"If-Match": `"${session.revision}"`}});
    el.error.textContent = "";
  } catch (error) {
    showError(error);
    try { session = await api(`/api/sessions/${sessionID}`); } catch (_) {}
  }
  render(true);
  renderCheck();
});

load();
setInterval(async () => {
  if (!session || (!exporting() && !publishing())) return;
  const wasExporting = exporting(), wasPublishing = publishing();
  try {
    session = await api(`/api/sessions/${sessionID}`);
    render();
    if ((wasExporting && !exporting()) || (wasPublishing && !publishing())) await refreshCheck(false);
  } catch (_) {}
}, 1500);
