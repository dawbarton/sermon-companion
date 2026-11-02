-- Runs one of the OBS script's actions outside OBS for the Go tests:
-- luajit harness.lua SCRIPT ADDRESS ACTION TIMEOUT
-- and prints whether it succeeded and its message, separated by a tab.
local script, address, action, timeout = arg[1], arg[2], arg[3], tonumber(arg[4])
local m = dofile(script)
local ok, message
if action == "start" then
  ok, message = m.start_recording(address, timeout)
else
  ok, message = m.stop_recording(address, timeout)
end
io.write(tostring(ok), "\t", message, "\n")
