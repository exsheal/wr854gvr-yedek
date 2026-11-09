@echo off
chcp 65001 >nul
title WR854GVR Yedek Al
cd /d "%~dp0"

if not exist "WR854GVR-Yedek.exe" (
    echo WR854GVR-Yedek.exe bulunamadi.
    echo Bu .bat dosyasini .exe ile ayni klasore koyun.
    echo.
    pause
    exit /b 1
)

WR854GVR-Yedek.exe
set RC=%ERRORLEVEL%

echo.
echo ======================================================
if "%RC%"=="0" (
    echo   Bitti. Cikis kodu: 0
) else (
    echo   HATA ile bitti. Cikis kodu: %RC%
)
echo.
if exist "wr854-yedek-log.txt" (
    echo   Cikti su dosyaya da yazildi:
    echo   %~dp0wr854-yedek-log.txt
    echo   Bu dosyayi bize gonderebilirsiniz.
) else (
    echo   Log dosyasi olusturulamadi.
)
echo ======================================================
echo.
pause
