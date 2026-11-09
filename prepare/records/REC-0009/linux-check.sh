#!/bin/sh
# Linux のコンテナで配布物の実行ファイルを動かす（SPEC-0004 §受入範囲 の操作の並び）。
# 使い方: sh linux-check.sh <実行ファイル> <ラベル>
set -u
B="$1"
L="$2"
export PATH=/usr/bin:/bin
export HOME=/tmp/home
mkdir -p "$HOME"
FAILED=0
ok() { echo "OK   $L: $*"; }
ng() { echo "FAIL $L: $*"; FAILED=1; }
pidof_daemon() { grep -o '"pid": *[0-9]*' "$R/.prepare-dev/daemon.json" 2>/dev/null | grep -o '[0-9]*$'; }

for t in go node npm pnpm; do
  if command -v "$t" >/dev/null 2>&1; then ng "$t is on PATH"; fi
done
ok "PATH=$PATH; go, node, npm, pnpm are not on it; uname -m: $(uname -m)"

cp "$B" /tmp/prepare-dev && chmod +x /tmp/prepare-dev
P=/tmp/prepare-dev
if V=$($P version --json); then ok "version --json: $(echo "$V" | tr -d '\n ' | cut -c1-160)"; else ng "version --json"; fi

R=/tmp/proj
mkdir -p "$R"
cd "$R" || exit 1
if $P init --root "$R" >/tmp/init.json 2>&1; then ok "init"; else ng "init: $(cat /tmp/init.json)"; fi
n=$(ls "$R/.agents/skills" | wc -l)
if [ "$n" -ge 7 ]; then ok "init copied $n skills into .agents/skills and $(ls "$R/.claude/skills" | wc -l) into .claude/skills"; else ng "skills: $n"; fi
if [ ! -e "$R/.mcp.json" ] && [ ! -e "$R/.codex/config.toml" ]; then ok "init wrote no MCP configuration"; else ng "init wrote an MCP configuration"; fi

printf 'r\n' >/tmp/req.md
if $P record create --root "$R" --kind requirement --title R --body-file /tmp/req.md >/tmp/rec.json 2>&1; then ok "record create: $(grep -o '"id": *"REQ-[0-9]*"' /tmp/rec.json | head -1)"; else ng "record create: $(cat /tmp/rec.json)"; fi
printf '# Q\n\n選ぶ。\n' >/tmp/q.md
echo '[{"key":"a","label":"A","summary":"a"},{"key":"b","label":"B","summary":"b"}]' >/tmp/o.json
if $P q create --root "$R" --subject REQ-0001 --prompt-file /tmp/q.md --options-file /tmp/o.json >/tmp/q.json 2>&1; then ok "q create: $(grep -o '"url": *"[^"]*"' /tmp/q.json)"; else ng "q create: $(cat /tmp/q.json)"; fi

URL=$($P ui --no-open --root "$R")
if wget -q -O /tmp/page.html "$URL/q/Q-0001" && grep -qi '<html' /tmp/page.html; then ok "the decision screen is served at $URL/q/Q-0001 ($(wc -c </tmp/page.html) bytes)"; else ng "decision screen"; fi

if wget -q -O /tmp/a0 --header 'Content-Type: application/json' --post-data '{"choice":["a"],"text":null}' "$URL/api/questions/Q-0001/answers" 2>/tmp/a0.err; then
  ng "an answer without Origin was accepted"
else
  ok "an answer without Origin is refused: $(grep -o 'HTTP/1.1 [0-9]* [A-Za-z ]*' /tmp/a0.err | head -1)"
fi
if wget -q -O /tmp/a1 --header "Origin: $URL" --header 'Content-Type: application/json' --post-data '{"choice":["a"],"text":null}' "$URL/api/questions/Q-0001/answers" 2>/tmp/a1.err; then ok "an answer with the page's Origin is accepted"; else ng "answer with Origin: $(cat /tmp/a1.err)"; fi
if $P q answer --root "$R" Q-0001 a >/tmp/qa.out 2>&1; then ng "q answer exists"; else ok "there is no q answer: $(head -1 /tmp/qa.out | cut -c1-120)"; fi

if $P q wait --root "$R" Q-0001 --timeout 5000 >/tmp/w.json 2>&1 && grep -q answered /tmp/w.json; then ok "q wait: answered"; else ng "q wait: $(cat /tmp/w.json)"; fi
AID=$(grep -o '"answer_id": *"ANS-[0-9-]*"' /tmp/w.json | head -1 | grep -o 'ANS-[0-9-]*')
if $P q ack --root "$R" Q-0001 "$AID" >/dev/null 2>&1; then ok "q ack $AID"; else ng "q ack $AID"; fi
printf '## 決定\n\na\n' >/tmp/d.md
if $P q apply --root "$R" Q-0001 "$AID" --title D --body-file /tmp/d.md --chosen a >/tmp/ap.json 2>&1 && grep -q DEC-0001 /tmp/ap.json; then ok "q apply made DEC-0001"; else ng "q apply: $(cat /tmp/ap.json)"; fi
if $P trace --root "$R" REQ-0001 >/tmp/tr.json 2>&1 && grep -q DEC-0001 /tmp/tr.json; then ok "trace REQ-0001 shows DEC-0001"; else ng "trace: $(head -c 300 /tmp/tr.json)"; fi
if $P bundle --root "$R" REQ-0001 >/tmp/b.json 2>&1 && grep -q '"path"' /tmp/b.json; then ok "bundle: $(grep -o '"path": *"[^"]*"' /tmp/b.json)"; else ng "bundle: $(cat /tmp/b.json)"; fi
ok "ui --no-open: $URL"

$P serve --root "$R" >/tmp/s2.out 2>&1 &
S2=$!
sleep 2
if kill -0 "$S2" 2>/dev/null; then ng "a second serve is still running"; kill "$S2"; else ok "a second serve ended on the lease: $(tail -1 /tmp/s2.out | cut -c1-160)"; fi

PID1=$(pidof_daemon)
kill -9 "$PID1"
sleep 1
if $P status --root "$R" >/tmp/st1.out 2>&1; then ok "status after kill -9 of the daemon (pid $PID1)"; else ng "status after kill -9: $(cat /tmp/st1.out)"; fi
PID2=$(pidof_daemon)
if [ -n "$PID2" ] && [ "$PID2" != "$PID1" ] && kill -0 "$PID2" 2>/dev/null; then ok "a new daemon started (pid $PID2)"; else ng "no new daemon after kill -9 ($PID2)"; fi

rm -f "$R/.prepare-dev/lease" "$R/.prepare-dev/daemon.json"
i=0
while kill -0 "$PID2" 2>/dev/null && [ "$i" -lt 30 ]; do sleep 0.1; i=$((i + 1)); done
if kill -0 "$PID2" 2>/dev/null; then ng "the daemon $PID2 still runs 3 s after its lease and daemon.json were removed"; else ok "the daemon $PID2 ended within $((i * 100)) ms after its lease and daemon.json were removed"; fi
if $P status --root "$R" >/tmp/st2.out 2>&1; then ok "status after the removal"; else ng "status after the removal: $(cat /tmp/st2.out)"; fi
PID3=$(pidof_daemon)
if [ -n "$PID3" ] && kill -0 "$PID3" 2>/dev/null; then ok "a new daemon started (pid $PID3)"; else ng "no new daemon after the removal"; fi
$P stop --root "$R" >/dev/null 2>&1
if [ "$FAILED" = 0 ]; then echo "RESULT $L: all passed"; else echo "RESULT $L: failed"; fi
