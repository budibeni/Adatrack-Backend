#!/usr/bin/env bash
# Health monitor — log memory/disk setiap 5 menit ke ~/wsl_health.log
# Berguna untuk diagnose penyebab WSL disconnect (OOM? disk full?).
while true; do
  echo "$(date -Is) | RAM: $(free -m | awk '/Mem/{printf "%d/%dMB (avail %dMB)",$3,$2,$7}') | Disk_WSL: $(df -h / | awk 'NR==2{print $3"/"$2" (avail "$5")"}') | Disk_Docker_VHDX: $(du -sh /mnt/c/Users/User/AppData/Local/Docker/wsl/disk/docker_data.vhdx 2>/dev/null | cut -f1) | Docker: $(docker ps --format '{{.Names}}' 2>/dev/null | wc -l) containers" >> ~/wsl_health.log
  sleep 300
done
