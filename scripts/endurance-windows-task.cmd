@echo off
rem ============================================================
rem  EnduranceWatchdog - Auto-start at Windows logon / WSL boot
rem  Launches WSL watchdog (auto-resume chunked endurance)
rem  Run once as Administrator to register Task Scheduler:
rem    schtasks /create /tn EnduranceWatchdog /tr "cmd /c \"%~f0\"" /sc onlogon /rl highest /f
rem ============================================================
setlocal
echo [%date% %time%] Starting WSL endurance watchdog
wsl.exe -d Ubuntu -e bash -lc "cd /home/user/projects/ajb_gps/backend/scripts && nohup ./endurance-watchdog.sh >> /home/user/b4_chunked_endurance/watchdog.log 2>&1 < /dev/null & disown"
