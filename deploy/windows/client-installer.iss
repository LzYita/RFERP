; RFERP 客户端安装包（Inno Setup 6）
;
; 为什么需要它：客户端是「装到客户电脑上、非开发人员操作」的交付物，
; 不能靠解压 zip 再双击 exe——那样杀软更容易拦、快捷方式要手工建、
; 安装位置也无从约束（方案 §8 / FR-43）。
;
; 编译：
;   ISCC.exe /DAppVersion=1.0.0 /DOutDir=dist deploy\windows\client-installer.iss
;
; 支持静默预配置（售后批量部署用）：
;   RFERP-setup.exe /VERYSILENT /SERVER=192.168.1.10:8080
; 会把 mode=client 写进 %APPDATA%\RFERP\config.json，首次启动直接是登录页。

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
#ifndef OutDir
  #define OutDir "..\..\dist"
#endif

#define AppName "RFERP"
#define AppExe "RFERP.exe"
#define AppPublisher "仁风仓库管理系统"

[Setup]
AppId={{6C1F6B33-6F0E-4E4C-9F4B-3B1D2C0A7E51}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
; 默认按机器安装；非管理员会自动转为按用户安装（装到 LocalAppData）。
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog
DefaultDirName={autopf}\RFERP
DefaultGroupName=RFERP
DisableProgramGroupPage=yes
; 客户端不需要重启，也不改系统组件。
ChangesAssociations=no
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
OutputDir={#OutDir}
OutputBaseFilename=RFERP-{#AppVersion}-setup
SetupIconFile=..\..\picture\app.ico
UninstallDisplayIcon={app}\{#AppExe}
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible

[Languages]
Name: "chinesesimplified"; MessagesFile: "ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加任务:"; Flags: checkedonce

[Files]
Source: "{#OutDir}\{#AppExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "client.config.json"; DestDir: "{app}"; DestName: "config.example.json"; Flags: ignoreversion

[Icons]
Name: "{group}\RFERP"; Filename: "{app}\{#AppExe}"
Name: "{autodesktop}\RFERP"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon

[Run]
; 只有交互安装才启动；静默部署通常由后续脚本统一拉起。
Filename: "{app}\{#AppExe}"; Description: "启动 RFERP"; Flags: nowait postinstall skipifsilent

[UninstallDelete]
; 只删程序目录。配置、日志与缓存留在 %APPDATA%\RFERP：
; 卸载重装后"连的是哪台主机"不该丢，用户也不需要重新填一遍。
Type: filesandordirs; Name: "{app}"

[Code]
// /SERVER=host[:port] 静默写入客户端配置。
//
// 校验在 RFERP 自己那边（/api/setup/connect-server）会再做一次探测；
// 这里只做"非空、不含空白"的粗筛，避免把明显错误的参数写进配置。
function GetServerParam: String;
var
  i: Integer;
  p: String;
begin
  Result := '';
  for i := 1 to ParamCount do
  begin
    p := ParamStr(i);
    if CompareText(Copy(p, 1, 8), '/SERVER=') = 0 then
      Result := Trim(Copy(p, 9, Length(p) - 8));
  end;
end;

procedure WriteClientConfig(Server: String);
var
  Dir, Json: String;
begin
  if Server = '' then
    exit;
  Dir := ExpandConstant('{userappdata}\RFERP');
  ForceDirectories(Dir);
  Json := '{"mode":"client","serverUrl":"' + Server + '","dataDir":"' +
          ExpandConstant('{localappdata}\RFERP\client') + '"}';
  SaveStringToFile(Dir + '\config.json', Json, False);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
    WriteClientConfig(GetServerParam);
end;
