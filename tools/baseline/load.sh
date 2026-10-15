#!/bin/bash
# Load test used for docs/go-migration.md: every feed once, then 3 rounds of all
# feeds in parallel, sampling container memory. Usage: load.sh CONTAINER PORT LOGFILE
# Feed ids come from the production copy at ~/Prosjekter/kringkasting-data/.
set -u
NAME=$1; PORT=$2; LOG=$3
DB="file:$HOME/Prosjekter/kringkasting-data/kringkasting-copy.sqlite3?immutable=1"
ids=()
while IFS= read -r line; do ids+=("$line"); done < <(sqlite3 "$DB" "select s.id from series s left join episodes e on e.series_id=s.id group by s.id order by count(e.id) desc")
( while true; do docker stats --no-stream --format '{{.MemUsage}}' "$NAME" | cut -d/ -f1; sleep 1; done ) > "$LOG" 2>/dev/null &
sampler=$!
start=$(date +%s); bytes=0; codes=""
for id in "${ids[@]}"; do
  out=$(curl -s -o /dev/null --max-time 120 -w '%{http_code} %{size_download}' "http://localhost:$PORT/api/feeds/$id")
  codes+="${out%% *} "; bytes=$((bytes + ${out##* }))
done
first=$(( $(date +%s) - start ))
for round in 1 2 3; do
  pids=()
  for id in "${ids[@]}"; do curl -s -o /dev/null --max-time 120 "http://localhost:$PORT/api/feeds/$id" & pids+=($!); done
  wait "${pids[@]}"
done
sleep 3; kill $sampler
echo "feeds=${#ids[@]} first_pass=${first}s bytes=$bytes total=$(( $(date +%s) - start ))s"
echo "status codes: $(tr ' ' '\n' <<<"$codes" | sort | uniq -c | tr '\n' ' ')"
echo "peak: $(sort -h "$LOG" | tail -1)"
sleep 10; echo "settled after 10s: $(docker stats --no-stream --format '{{.MemUsage}}' "$NAME")"
