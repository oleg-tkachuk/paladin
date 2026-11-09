-- One key's sliding window, decided on the server's clock (ADR-0046).
--
-- KEYS[1]  the key's hash: field = bucket index, value = count.
-- ARGV[1]  bucket width, microseconds.
-- ARGV[2]  mode: 0 read, 1 count always, 2 count only when it fits.
-- ARGV[3]  capacity over the trailing window (mode 2).
-- ARGV[4]  key time-to-live, milliseconds.
--
-- Fields are bucket indexes rather than start times: an index is small
-- enough that tostring keeps every digit, where a microsecond timestamp
-- would print in exponent form and two buckets could share a field.
--
-- Returns { admitted, current, previous, elapsed_us, index }.
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])
local width = tonumber(ARGV[1])
local mode = tonumber(ARGV[2])
local index = math.floor(now / width)
local elapsed = now - index * width
local cur_f = tostring(index)
local prev_f = tostring(index - 1)
local cur = tonumber(redis.call('HGET', KEYS[1], cur_f) or '0')
local prev = tonumber(redis.call('HGET', KEYS[1], prev_f) or '0')
local admitted = 0
if mode == 1 then
  cur = redis.call('HINCRBY', KEYS[1], cur_f, 1)
  admitted = 1
elseif mode == 2 then
  local share = prev * (1 - elapsed / width)
  if cur + 1 + share <= tonumber(ARGV[3]) then
    cur = redis.call('HINCRBY', KEYS[1], cur_f, 1)
    admitted = 1
  end
end
if mode ~= 0 then
  if redis.call('HLEN', KEYS[1]) > 2 then
    for _, f in ipairs(redis.call('HKEYS', KEYS[1])) do
      if f ~= cur_f and f ~= prev_f then
        redis.call('HDEL', KEYS[1], f)
      end
    end
  end
  redis.call('PEXPIRE', KEYS[1], tonumber(ARGV[4]))
end
return { admitted, cur, prev, elapsed, index }
