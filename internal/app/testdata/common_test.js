"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");

global.window = {};
require("../static/common.js");
const {suggestSegmentRange} = window.SC;

test("a cursor inside a segment moves to the first following gap", () => {
  const range = suggestSegmentRange(15, 100, [
    {startSeconds: 10, endSeconds: 20},
    {startSeconds: 20, endSeconds: 30},
    {startSeconds: 50, endSeconds: 60}
  ]);
  assert.deepEqual(range, {start: 30, end: 50});
});

test("a cursor already in a gap stays at the cursor", () => {
  const range = suggestSegmentRange(25, 100, [
    {startSeconds: 10, endSeconds: 20},
    {startSeconds: 40, endSeconds: 50}
  ]);
  assert.deepEqual(range, {start: 25, end: 40});
});

test("archived segments do not reserve time", () => {
  const range = suggestSegmentRange(15, 100, [
    {startSeconds: 10, endSeconds: 20, archived: true}
  ]);
  assert.deepEqual(range, {start: 15, end: 75});
});

test("no range is suggested when every later time is occupied", () => {
  const range = suggestSegmentRange(95, 100, [
    {startSeconds: 90, endSeconds: 100}
  ]);
  assert.equal(range, null);
});
