#!/bin/sh
set -eu

cd "$(dirname "$0")"
umask 077
if [ ! -s data/mobile-key ]; then
  openssl rand -hex 24 > data/mobile-key
fi

lan_ip=""
for iface in en0 en1; do
  lan_ip="$(ipconfig getifaddr "$iface" 2>/dev/null || true)"
  if [ -n "$lan_ip" ]; then break; fi
done

if [ -n "$lan_ip" ]; then
  printf '手机端地址: http://%s:8787\n' "$lan_ip"
else
  printf '请在电脑网络设置中查看局域网 IPv4 地址，并在手机端输入 http://电脑IP:8787\n'
fi
printf '连接密钥: %s\n' "$(cat data/mobile-key)"
printf '保持此终端运行，手机与电脑需要处于同一局域网。\n'

ANISHELF_MOBILE_KEY="$(cat data/mobile-key)" go run ./cmd/server -addr 0.0.0.0:8787
