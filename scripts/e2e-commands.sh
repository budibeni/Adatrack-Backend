#!/usr/bin/env bash
# ============================================================================
# e2e-commands.sh — end-to-end verification of the B8 downlink command path
# ============================================================================
# Simulated GT06 device → login → ingestion-tcp writes the 0x80 online command →
# device replies 0x21 "DYD=Success!" → td_device_commands flips to `acked`.
#
# Also checks the two non-delivery outcomes that must be RECORDED, never dropped:
#   * a device with no live connection               → status `offline`
#   * a command published while ingestion is stopped → delivered on restart
#     (durable JetStream consumer, B8 audit gap "core NATS")
#
# Usage: scripts/e2e-commands.sh [--no-durable-check]
# Prerequisites: infra reachable (PostgreSQL/Redis/NATS) + migrations applied.
# ============================================================================
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
# shellcheck source=scripts/lib-db.sh
. "$ROOT/scripts/lib-db.sh"

VARIANT="${COMPOSE_VARIANT:-local}"
load_variant_env "$VARIANT"

COMPANY="${E2E_COMPANY:-DEV001}"
IMEI="${E2E_IMEI:-864201040512345}"
VEHICLE_ID="${E2E_VEHICLE_ID:-1}"
TCP_PORT_DEV="${TCP_PORT:-9003}"
SCHEMA="$(echo "$COMPANY" | tr '[:upper:]' '[:lower:]')"
COMPANY_SCHEMA="${COMPANY_DB_PREFIX:-adatrack_gps_}${SCHEMA}"
DURABLE_CHECK=true

for arg in "$@"; do
  case "$arg" in
    --no-durable-check) DURABLE_CHECK=false ;;
  esac
done

PASS=0
FAIL=0
ok()  { echo "[PASS] $1"; PASS=$((PASS + 1)); }
bad() { echo "[FAIL] $1"; FAIL=$((FAIL + 1)); }

psql_q() { psql -tAc "$1" 2>/dev/null; }
cmd_status() { # request_id → status
  psql_q "SELECT status FROM ${COMPANY_SCHEMA}.td_device_commands WHERE request_id='$1'" | tr -d '[:space:]'
}
cmd_detail() { # request_id → detail
  psql_q "SELECT detail FROM ${COMPANY_SCHEMA}.td_device_commands WHERE request_id='$1'" | tr -d '[:space:]'
}
wait_status() { # request_id expected_status
  local req="$1" want="$2" got=""
  for _ in $(seq 1 30); do
    got="$(cmd_status "$req")"
    [[ "$got" == "$want" ]] && return 0
    sleep 1
  done
  echo "  (last status: '${got}', want '${want}')" >&2
  return 1
}
publish() { # kind imei → echoes request_id
  # Running on the host: the env file points NATS at the compose service name, so the
  # published host port is used instead (same rule as lib-db.sh applies to PostgreSQL).
  local nats_url="nats://127.0.0.1:${HOST_NATS_PORT:-4222}"
  (cd "$ROOT/tools/jsadmin" && go run . --nats "$nats_url" --publish-command "$1" \
      --imei "$2" --vehicle-id "$VEHICLE_ID" --company "$COMPANY" |
      sed -n 's/.*request_id=\([0-9a-f]*\).*/\1/p')
}

echo "e2e-commands: variant=$VARIANT company=$COMPANY imei=$IMEI"
echo "e2e-commands: migrations"
"$ROOT/scripts/migrate.sh" "$VARIANT" >/dev/null

echo "e2e-commands: starting pipeline services (host mode)"
"$ROOT/scripts/start-services.sh" up >/dev/null
for _ in $(seq 1 30); do
  curl -fsS "http://127.0.0.1:8090/healthz" >/dev/null 2>&1 && break
  sleep 1
done
curl -fsS "http://127.0.0.1:8090/healthz" >/dev/null && echo "e2e-commands: ingestion-tcp ready"


# --- device simulator --------------------------------------------------------
# GT06 login → read the 0x80 command → reply 0x21 with a canned ACK content.
SIM_LOG="$(mktemp /tmp/e2e-commands-sim.XXXXXX)"
SIM_SCRIPT="$(mktemp /tmp/e2e-commands-sim.XXXXXX.py)"
cat > "$SIM_SCRIPT" <<'PY'
import socket, struct, sys, time

TCP_PORT, IMEI, LOG = int(sys.argv[1]), sys.argv[2].encode(), sys.argv[3]

def crc16(data: bytes) -> int:
    """CRC-ITU (CRC-16/X-25): reflected poly 0x8408, init 0xffff, final xor 0xffff."""
    fcs = 0xFFFF
    for b in data:
        fcs ^= b
        for _ in range(8):
            fcs = (fcs >> 1) ^ 0x8408 if fcs & 1 else fcs >> 1
    return (~fcs) & 0xFFFF

def frame(proto: int, content: bytes) -> bytes:
    payload = bytes([proto]) + content
    body = bytes([len(payload)]) + payload
    return b"\x78\x78" + body + struct.pack(">H", crc16(body)) + b"\x0d\x0a"

def log(msg: str) -> None:
    with open(LOG, "a") as fh:
        fh.write(msg + "\n")

s = socket.create_connection(("127.0.0.1", TCP_PORT), timeout=15)
s.sendall(frame(0x01, IMEI + b"\x00\x01" + b"\x00\x01"))  # login: IMEI + model + serial
login_ack = s.recv(64)
log("login_ack=" + login_ack.hex())

s.settimeout(45)
try:
    cmd = s.recv(256)
except socket.timeout:
    log("error=no command received")
    sys.exit(1)
log("command_frame=" + cmd.hex())

# Online-command reply (0x21): server flag(4) + content code + ASCII content.
ack = frame(0x21, b"\x00\x00\x00\x00" + b"\x01" + b"DYD=Success!" + b"\x00\x02" + b"\x00\x07")
s.sendall(ack)
log("ack_frame=" + ack.hex())
time.sleep(1.5)
s.close()
PY

echo "e2e-commands: starting device simulator on :$TCP_PORT_DEV"
python3 "$SIM_SCRIPT" "$TCP_PORT_DEV" "$IMEI" "$SIM_LOG" &
SIM_PID=$!
sleep 3

REQ1="$(publish engine_cut "$IMEI" || true)"
if [[ -z "$REQ1" ]]; then
  bad "publish engine_cut produced no request_id"
elif wait_status "$REQ1" acked; then
  detail="$(cmd_detail "$REQ1")"
  ok "command delivered + device ACK recorded (request_id=$REQ1 detail='$detail')"
else
  bad "command $REQ1 did not reach status acked"
fi

if grep -q '^command_frame=' "$SIM_LOG"; then
  ok "device received the server command frame ($(sed -n 's/^command_frame=//p' "$SIM_LOG" | head -1 | cut -c1-24)…)"
else
  bad "device never received a command frame"
fi
if grep -q '^ack_frame=' "$SIM_LOG"; then
  ok "device replied with the 0x21 online-command reply"
else
  bad "device did not reply"
fi
wait "$SIM_PID" 2>/dev/null || true

# --- non-delivery outcome: a device that is NOT connected --------------------
REQ2="$(publish reboot 864201040599999 || true)"
if [[ -n "$REQ2" ]]; then
  if wait_status "$REQ2" offline; then
    ok "offline device recorded as status=offline (request_id=$REQ2)"
  else
    bad "offline command $REQ2 was not recorded as offline"
  fi
fi

# --- durability: publish while the service is DOWN ---------------------------
if [[ "$DURABLE_CHECK" == true ]]; then
  "$ROOT/scripts/start-services.sh" down >/dev/null 2>&1 || true
  sleep 1
  REQ3="$(publish locate "$IMEI" || true)"
  "$ROOT/scripts/start-services.sh" up >/dev/null 2>&1 || true
  for _ in $(seq 1 30); do
    curl -fsS "http://127.0.0.1:8090/healthz" >/dev/null 2>&1 && break
    sleep 1
  done
  if [[ -n "$REQ3" ]] && wait_status "$REQ3" offline; then
    ok "command published while ingestion-tcp was DOWN was delivered on restart (durable consumer, request_id=$REQ3)"
  else
    bad "durable delivery failed for $REQ3"
  fi
fi

rm -f "$SIM_SCRIPT" "$SIM_LOG"

echo
echo "e2e-commands summary: ${PASS}/$((PASS + FAIL)) checks passed"
[[ "$FAIL" -eq 0 ]]
