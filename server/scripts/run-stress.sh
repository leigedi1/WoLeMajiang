#!/bin/bash
# 阶段9 最终采集: 3 房 x 4 人 (12 连接) 240s, 同时采样 CPU/RSS
set -u
cd /root/mj-stress || exit 1
pkill -f majiang-server-linux 2>/dev/null
sleep 1
rm -f stress.db stress.db-wal stress.db-shm server.log stress.json client-acts.txt samples.csv

MAJIANG_HTTP_LISTEN=127.0.0.1:18791 \
MAJIANG_DB_PATH=/root/mj-stress/stress.db \
MAJIANG_ALLOWED_ORIGINS=http://127.0.0.1:18791 \
MAJIANG_MAX_ONLINE_ROOMS=3 \
nohup ./majiang-server-linux > server.log 2>&1 < /dev/null &
SRV=$!
sleep 2

./stress-linux -mode normal -base http://127.0.0.1:18791 -rooms 3 -players 4 -timeout 240s > stress.json 2> client-acts.txt &
STRESS=$!
START=$(date +%s%N)

echo "t,cpu_pct,rss_kb,load1" > samples.csv
PREV=$(awk '{print $14+$15}' /proc/$SRV/stat)
PREVT=$(date +%s%N)
while kill -0 $STRESS 2>/dev/null; do
  sleep 2
  NOW=$(date +%s%N)
  CUR=$(awk '{print $14+$15}' /proc/$SRV/stat)
  DT=$(( (NOW - PREVT) / 1000000 ))
  JIFF=$(( CUR - PREV ))
  CPU=$(awk -v j="$JIFF" -v d="$DT" 'BEGIN{ if (d<=0) {print 0} else {printf "%.1f", j*100000/(d*100)} }')
  RSS=$(awk '/VmRSS/{print $2}' /proc/$SRV/status)
  L1=$(awk '{print $1}' /proc/loadavg)
  T=$(( (NOW - START) / 1000000000 ))
  echo "$T,$CPU,$RSS,$L1" >> samples.csv
  PREV=$CUR
  PREVT=$NOW
done
wait $STRESS
echo "=== stress.json ==="
cat stress.json
echo "=== client-acts ==="
cat client-acts.txt
echo "=== samples(head/tail) ==="
head -8 samples.csv
tail -4 samples.csv
pkill -f majiang-server-linux
