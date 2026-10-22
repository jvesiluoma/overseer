Overscreen @VERSION@ for Windows
================================

Turns any browser (a car's browser, an iPad, a tablet, an old laptop) into an extra display for
this PC. Nothing to install on the other device.

Start
-----
1. Unzip this folder anywhere and double-click Overscreen.exe. There is no installer.
   Windows SmartScreen may say "Windows protected your PC" because the app is new and unsigned:
   click "More info", then "Run anyway".
2. Allow Overscreen through the Windows Firewall when asked (private networks are enough).
3. The status page opens in your browser with the address, a QR code and the pairing code.
   Later, use the Overscreen icon in the notification area (system tray).
4. On the other device, open the https:// address, accept the certificate warning (check that
   the fingerprint matches the status page), enter the pairing code and pick a display.

Extend or mirror
----------------
- Mirror main screen: streams your existing screen. Works without anything else.
- Extend (a separate virtual display): needs the free, signed Virtual Display Driver, installed
  once with administrator rights. Get the "Virtual Driver Control" app from
  https://github.com/VirtualDrivers/Virtual-Display-Driver/releases (or run
  winget install --id=VirtualDrivers.Virtual-Display-Driver -e), open it and click Install.
  Overscreen switches the virtual display on while a device is connected and off afterwards.
  Already installed but Overscreen still asks for it? Open Device Manager > Display adapters,
  right-click "Virtual Display Driver" and choose Enable device. No restart of Overscreen needed.
  The driver's authors advise uninstalling it before major graphics driver updates.

In a car or without Wi-Fi
-------------------------
Plug your phone in by USB and turn on USB tethering (iPhone: Personal Hotspot). Then turn on
Settings > Network & internet > Mobile hotspot, sharing the phone's connection over Wi-Fi, and
join that network in the car. The address becomes https://192.168.137.1.
Or join the phone's own hotspot with both the PC and the car.

Known limits of this preview
----------------------------
- Not yet tested on many PCs. Logs: %APPDATA%\Overscreen\overscreen.log
- Touch input cannot control windows of programs running as administrator.
- The lock screen and UAC prompts are not streamed (Windows hides them from screen capture).

Licences
--------
Overscreen is free software under the GNU AGPL v3 (LICENSE.txt).
Source: https://github.com/jvesiluoma/overseer
ffmpeg.exe is FFmpeg @FFMPEG_VERSION@ built by https://github.com/BtbN/FFmpeg-Builds
(release @FFMPEG_TAG@), licensed under the GNU GPL v3 (FFMPEG-LICENSE.txt). Its source code is
available from https://github.com/FFmpeg/FFmpeg and the build scripts from the BtbN repository.
