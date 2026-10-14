@echo off
setlocal
chcp 65001 >nul
cd /d "%~dp0"

rem nfentik-go 启动脚本
rem 连接串优先级：环境变量 > config\config.json
rem 首次运行（未配置数据库或管理员令牌为空）会进入安装向导：
rem   浏览器访问 http://127.0.0.1:3000/setup
rem 如需允许局域网访问，请保留下面的 -lan 参数；仅本机访问可去掉。

echo 正在启动 nfentik-go ...
echo 服务地址：http://127.0.0.1:3000
echo 控制台：  http://127.0.0.1:3000/console
echo.

"%~dp0nfentik-go.exe" -port 3000 -lan

echo.
echo nfentik-go 已退出，退出码 %ERRORLEVEL%
pause
