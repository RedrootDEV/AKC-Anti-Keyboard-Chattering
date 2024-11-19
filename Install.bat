@echo off
setlocal

set "AKC_DIR=C:\AKC"
set "AKC_EXE=AKC.exe"
set "CONFIG_FILE=config.json"
set "STARTUP_DIR=%AppData%\Microsoft\Windows\Start Menu\Programs\Startup"
set "SHORTCUT_NAME=AKC.lnk"

:: Menu for user action
echo --------------------------------------------------
echo Welcome to AKC Installer
echo --------------------------------------------------
echo 1. Install AKC (C:\AKC\AKC.exe)
echo 2. Uninstall AKC
echo 3. Exit
echo --------------------------------------------------
set /p option="Please choose an option (1, 2, or 3): "

if "%option%"=="1" goto :install
if "%option%"=="2" goto :uninstall
if "%option%"=="3" exit

echo Invalid option, please select 1, 2, or 3.
goto :end

:install
echo Installing AKC...

:: Create C:\AKC folder if it doesn't exist
if not exist "%AKC_DIR%" (
    mkdir "%AKC_DIR%"
    echo Folder %AKC_DIR% created.
) else (
    echo The folder %AKC_DIR% already exists.
)

:: Copy AKC.exe and config.json to C:\AKC
echo Copying the files...
copy /Y "%AKC_EXE%" "%AKC_DIR%\%AKC_EXE%" >nul
copy /Y "%CONFIG_FILE%" "%AKC_DIR%\%CONFIG_FILE%" >nul

:: Create a shortcut in the user's Startup folder
echo Creating a shortcut in the user's Startup folder...
call :createShortcut "%STARTUP_DIR%\%SHORTCUT_NAME%" "%AKC_DIR%\%AKC_EXE%"

:: Wait briefly and run AKC.exe
timeout /t 2 >nul
if exist "%AKC_DIR%\%AKC_EXE%" (
    echo Running AKC.exe...
    cd /d "%AKC_DIR%" && start "" "%AKC_EXE%"
) else (
    echo ERROR: Unable to find AKC.exe in %AKC_DIR%. Installation incomplete.
    goto :end
)

echo Installation completed successfully.
goto :end

:uninstall
echo Uninstalling AKC...

:: Kill AKC.exe if it's running
echo Checking if AKC.exe is running...
taskkill /F /IM "%AKC_EXE%" >nul 2>&1

:: Remove the shortcut from the user's Startup folder
echo Removing the shortcut from the user's Startup folder...
del "%STARTUP_DIR%\%SHORTCUT_NAME%" >nul 2>&1

:: Delete the AKC folder
echo Deleting the AKC folder...
rmdir /S /Q "%AKC_DIR%"

echo Uninstallation completed successfully.
goto :end

:createShortcut
:: Parameters: %1 = Shortcut Path, %2 = Target Path
set "WScriptFile=%TEMP%\CreateShortcut.vbs"
echo Set WshShell = WScript.CreateObject("WScript.Shell") > "%WScriptFile%"
echo Set Shortcut = WshShell.CreateShortcut(WScript.Arguments(0)) >> "%WScriptFile%"
echo Shortcut.TargetPath = WScript.Arguments(1) >> "%WScriptFile%"
echo Shortcut.WorkingDirectory = CreateObject("Scripting.FileSystemObject").GetParentFolderName(WScript.Arguments(1)) >> "%WScriptFile%"
echo Shortcut.Save >> "%WScriptFile%"
cscript //nologo "%WScriptFile%" "%~1" "%~2"
del "%WScriptFile%"
goto :eof

:end
endlocal
