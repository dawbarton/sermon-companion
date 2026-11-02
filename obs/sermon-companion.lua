-- Sermon Companion's OBS script: starts Sermon Companion's recording when OBS
-- starts streaming, and stops it when the stream stops, so that nobody has to
-- remember to press Start Recording in the dock. It can follow OBS's own
-- recording instead of, or as well as, the stream.
--
-- Add it once in OBS under Tools, Scripts, with the + button. It talks only to
-- Sermon Companion on this computer, through the same local requests the dock
-- makes, so it needs neither OBS WebSocket nor Python. What it does, and any
-- problem it meets, is written to the script log (Tools, Scripts, Script Log).
--
-- Stopping from here never asks for confirmation: the dock's question guards
-- against a stray click, and the end of the stream is not one.

local ffi = require("ffi")
local bit = require("bit")

-- obslua is the OBS API, present only inside OBS. Outside it, as in the
-- tests, the functions this script returns can still be called.
local obs = obslua

local DEFAULT_ADDRESS = "http://127.0.0.1:8765"
-- How long a request may take. Starting waits for the first audio from the
-- capture device, and stopping for the recording to be closed; both normally
-- take about a second. OBS waits while a request is answered.
local DEFAULT_TIMEOUT_SECONDS = 15

local current = {
  address = DEFAULT_ADDRESS,
  follow_streaming = true,
  follow_recording = false,
}

local function log(message, warning)
  if obs then
    obs.script_log(warning and obs.LOG_WARNING or obs.LOG_INFO, message)
  else
    print(message)
  end
end

-- Sockets -------------------------------------------------------------------

local windows = ffi.os == "Windows"
local bsd = ffi.os == "OSX" or ffi.os == "BSD"

local sockets
local constants

-- The C declarations are made once per Lua state; a second declaration of
-- the same function is an error in LuaJIT.
pcall(ffi.cdef, windows and [[
  typedef uintptr_t SOCKET;
  int WSAStartup(unsigned short version, void *data);
  SOCKET socket(int af, int type, int protocol);
  int connect(SOCKET s, const void *address, int length);
  int send(SOCKET s, const char *buffer, int length, int flags);
  int recv(SOCKET s, char *buffer, int length, int flags);
  int closesocket(SOCKET s);
  int setsockopt(SOCKET s, int level, int option, const void *value, int length);
  int WSAGetLastError(void);
]] or [[
  int socket(int domain, int type, int protocol);
  int connect(int s, const void *address, unsigned int length);
  long send(int s, const void *buffer, size_t length, int flags);
  long recv(int s, void *buffer, size_t length, int flags);
  int close(int s);
  int setsockopt(int s, int level, int option, const void *value, unsigned int length);
]])

if windows then
  sockets = ffi.load("ws2_32")
  -- Winsock has to be started once per process; OBS has normally done so
  -- already, and starting it again is harmless.
  sockets.WSAStartup(0x0202, ffi.new("uint8_t[1024]"))
  constants = {sol_socket = 0xffff, so_rcvtimeo = 0x1006, so_sndtimeo = 0x1005, send_flags = 0}
elseif bsd then
  sockets = ffi.C
  -- SO_NOSIGPIPE keeps a write to a closed connection from killing OBS.
  constants = {sol_socket = 0xffff, so_rcvtimeo = 0x1006, so_sndtimeo = 0x1005, so_nosigpipe = 0x1022, send_flags = 0}
else
  sockets = ffi.C
  -- MSG_NOSIGNAL does the same on Linux.
  constants = {sol_socket = 1, so_rcvtimeo = 20, so_sndtimeo = 21, send_flags = 0x4000}
end

local AF_INET, SOCK_STREAM = 2, 1

-- address_bytes builds a sockaddr_in for an IPv4 address and port. It is
-- built byte by byte because its layout differs: BSD starts with a length.
local function address_bytes(host, port)
  local a, b, c, d = host:match("^(%d+)%.(%d+)%.(%d+)%.(%d+)$")
  if not a then
    return nil
  end
  local address = ffi.new("uint8_t[16]")
  if bsd then
    address[0], address[1] = 16, AF_INET
  else
    address[0], address[1] = AF_INET, 0
  end
  address[2], address[3] = bit.rshift(port, 8), bit.band(port, 0xff)
  address[4], address[5], address[6], address[7] = tonumber(a), tonumber(b), tonumber(c), tonumber(d)
  return address
end

local function set_timeouts(s, seconds)
  local value, length
  if windows then
    value, length = ffi.new("uint32_t[1]", seconds * 1000), 4
  elseif bsd then
    -- struct timeval: a 64-bit second count and a 32-bit microsecond count.
    value = ffi.new("int64_t[2]")
    value[0] = seconds
    length = 16
  else
    value = ffi.new("long[2]")
    value[0] = seconds
    length = ffi.sizeof(value)
  end
  sockets.setsockopt(s, constants.sol_socket, constants.so_rcvtimeo, value, length)
  sockets.setsockopt(s, constants.sol_socket, constants.so_sndtimeo, value, length)
  if constants.so_nosigpipe then
    sockets.setsockopt(s, constants.sol_socket, constants.so_nosigpipe, ffi.new("int[1]", 1), 4)
  end
end

-- parse_address reads an address such as http://127.0.0.1:8765 as a host and
-- a port. Only plain HTTP to an IPv4 address is supported, which is all the
-- local server offers.
local function parse_address(text)
  local host, port = tostring(text or ""):match("^%s*http://([^/:%s]+):?(%d*)/?%s*$")
  if not host then
    return nil, nil, "the address must look like " .. DEFAULT_ADDRESS
  end
  if host == "localhost" then
    host = "127.0.0.1"
  end
  port = tonumber(port) or 80
  if not address_bytes(host, port) or port < 1 or port > 65535 then
    return nil, nil, "the address must be a numeric IPv4 address and a port, such as " .. DEFAULT_ADDRESS
  end
  return host, port
end

-- request sends a small request and returns the HTTP status and body, or nil
-- and the reason it failed. It waits at most timeout seconds for each step.
local function request(address, method, path, body, timeout)
  local host, port, problem = parse_address(address)
  if not host then
    return nil, problem
  end
  body = body or ""
  local s = sockets.socket(AF_INET, SOCK_STREAM, 0)
  local invalid = windows and (s == ffi.cast("SOCKET", -1)) or (not windows and s < 0)
  if invalid then
    return nil, "could not open a network connection"
  end
  local function finish(...)
    if windows then
      sockets.closesocket(s)
    else
      sockets.close(s)
    end
    return ...
  end
  set_timeouts(s, timeout or DEFAULT_TIMEOUT_SECONDS)
  if sockets.connect(s, address_bytes(host, port), 16) ~= 0 then
    return finish(nil, "Sermon Companion is not running, or is not at " .. address)
  end
  local message = table.concat({
    method .. " " .. path .. " HTTP/1.1",
    "Host: " .. host .. ":" .. port,
    "User-Agent: Sermon Companion OBS script",
    "Content-Type: application/json",
    "Content-Length: " .. #body,
    "Connection: close",
    "",
    body,
  }, "\r\n")
  local sent = 0
  while sent < #message do
    local n = tonumber(sockets.send(s, ffi.cast("const char *", message) + sent, #message - sent, constants.send_flags))
    if n <= 0 then
      return finish(nil, "the request could not be sent")
    end
    sent = sent + n
  end
  local parts, buffer, total = {}, ffi.new("char[4096]"), 0
  while total < 1048576 do
    local n = tonumber(sockets.recv(s, buffer, 4096, 0))
    if n == 0 then
      break
    elseif n < 0 then
      if total == 0 then
        return finish(nil, "Sermon Companion did not answer within " .. (timeout or DEFAULT_TIMEOUT_SECONDS) .. " seconds")
      end
      break
    end
    parts[#parts + 1] = ffi.string(buffer, n)
    total = total + n
  end
  local response = table.concat(parts)
  local status = tonumber(response:match("^HTTP/%d%.%d (%d%d%d)"))
  if not status then
    return finish(nil, "the answer was not understood")
  end
  return finish(status, response:match("\r\n\r\n(.*)$") or "")
end

-- error_text finds the message in Sermon Companion's {"error": "..."} answer.
local function error_text(body)
  local text = body:match('"error"%s*:%s*"(.-)"%s*}')
  if not text then
    return "no reason was given"
  end
  return (text:gsub('\\"', '"'):gsub("\\u0026", "&"):gsub("\\u003c", "<"):gsub("\\u003e", ">"))
end

-- Starting and stopping -----------------------------------------------------

-- start_recording starts Sermon Companion's recording, leaving one already
-- running alone. It returns what it did, for the log.
local function start_recording(address, timeout)
  local status, body = request(address, "POST", "/api/sessions", "{}", timeout)
  if not status then
    return false, "Could not start Sermon Companion's recording: " .. body .. "."
  elseif status == 201 then
    return true, "Started Sermon Companion's recording."
  elseif status == 409 and body:find("already in progress", 1, true) then
    return true, "Sermon Companion was already recording, so that recording carries on."
  end
  return false, "Sermon Companion refused to start recording: " .. error_text(body) .. "."
end

-- stop_recording stops whatever Sermon Companion is recording.
local function stop_recording(address, timeout)
  local status, body = request(address, "POST", "/api/stop-recording", "{}", timeout)
  if not status then
    return false, "Could not stop Sermon Companion's recording: " .. body .. "."
  elseif status == 200 then
    return true, "Stopped Sermon Companion's recording."
  elseif status == 409 and body:find("nothing is being recorded", 1, true) then
    return true, "Sermon Companion was not recording, so there was nothing to stop."
  end
  return false, "Sermon Companion refused to stop recording: " .. error_text(body) .. "."
end

local function report(ok, message)
  log(message, not ok)
end

-- An output that is still running keeps the recording going: following both,
-- the end of OBS's own recording does not stop a service still streaming.
local function still_running()
  if current.follow_streaming and obs.obs_frontend_streaming_active() then
    return "the stream is still running"
  end
  if current.follow_recording and obs.obs_frontend_recording_active() then
    return "OBS is still recording"
  end
  return nil
end

local function on_event(event)
  local started = (current.follow_streaming and event == obs.OBS_FRONTEND_EVENT_STREAMING_STARTED)
    or (current.follow_recording and event == obs.OBS_FRONTEND_EVENT_RECORDING_STARTED)
  local stopped = (current.follow_streaming and event == obs.OBS_FRONTEND_EVENT_STREAMING_STOPPED)
    or (current.follow_recording and event == obs.OBS_FRONTEND_EVENT_RECORDING_STOPPED)
  if started then
    report(start_recording(current.address))
  elseif stopped then
    local reason = still_running()
    if reason then
      log("Sermon Companion's recording carries on, because " .. reason .. ".")
    else
      report(stop_recording(current.address))
    end
  end
end

-- OBS script interface ------------------------------------------------------

function script_description()
  return "<b>Sermon Companion</b><br>" ..
    "Starts Sermon Companion's recording when OBS starts streaming, and stops it when the stream stops. " ..
    "Sermon Companion must be running. What happens is written to the script log."
end

function script_properties()
  local properties = obs.obs_properties_create()
  obs.obs_properties_add_text(properties, "address", "Sermon Companion address", obs.OBS_TEXT_DEFAULT)
  obs.obs_properties_add_bool(properties, "follow_streaming", "Follow the stream")
  obs.obs_properties_add_bool(properties, "follow_recording", "Follow OBS's own recording")
  return properties
end

function script_defaults(settings)
  obs.obs_data_set_default_string(settings, "address", DEFAULT_ADDRESS)
  obs.obs_data_set_default_bool(settings, "follow_streaming", true)
  obs.obs_data_set_default_bool(settings, "follow_recording", false)
end

function script_update(settings)
  local address = obs.obs_data_get_string(settings, "address")
  local _, _, problem = parse_address(address)
  if problem then
    log("The Sermon Companion address " .. address .. " cannot be used: " .. problem .. ". " .. DEFAULT_ADDRESS .. " is used instead.", true)
    address = DEFAULT_ADDRESS
  end
  current.address = address
  current.follow_streaming = obs.obs_data_get_bool(settings, "follow_streaming")
  current.follow_recording = obs.obs_data_get_bool(settings, "follow_recording")
  if not current.follow_streaming and not current.follow_recording then
    log("Neither the stream nor OBS's recording is followed, so Sermon Companion will not be started or stopped.", true)
  end
end

function script_load(settings)
  obs.obs_frontend_add_event_callback(on_event)
end

function script_unload()
  obs.obs_frontend_remove_event_callback(on_event)
end

-- For the tests, which run this file outside OBS.
return {
  parse_address = parse_address,
  request = request,
  start_recording = start_recording,
  stop_recording = stop_recording,
  error_text = error_text,
}
