# Xendfile cross-platform alpha test checklist

This checklist records the real-machine evidence still required before a public
release. The current packages are unsigned development builds. Test only source
and artifacts from the project's GitHub repository, compare hashes when
provided, and do not dismiss operating-system security warnings for unrelated
downloads.

## Tester record

Copy this table once per machine and attach the completed result to the pull
request or an issue. Do not include usernames, IP addresses, device IDs,
pairing codes, tokens, private keys, filenames, or file contents.

| Field | Result |
| --- | --- |
| Date and tester | |
| OS and exact version | |
| Architecture (`amd64` or `arm64`) | |
| Desktop environment, if Linux | |
| Source commit | |
| Fresh install or upgrade | |
| Result | Pass / Fail / Blocked |
| Sanitized notes | |

## Windows 10/11

1. Download the source snapshot for the exact commit and verify its published
   SHA-256 value.
2. Extract it, open PowerShell in the extracted folder, and run
   `powershell -ExecutionPolicy Bypass -File .\install.ps1`.
3. Confirm **Xendfile** appears in Start and the notification area, with no
   unexpected administrator prompt. A private-network firewall prompt is
   expected for this unsigned alpha.
4. Open a new PowerShell window and run `xendfile --version` and
   `xendfile peers`.
5. Pair with the Linux test machine, send a small non-sensitive file in each
   direction, verify the contents byte-for-byte, and decline one incoming file.
6. Run the installer again to test an in-place upgrade. Confirm the pairing is
   preserved and only one tray process remains.
7. Open **Uninstall Xendfile** from Start. Confirm the app, shortcuts, startup
   entry, and PATH entry disappear while received files remain.
8. Reinstall and repeat one transfer. Record any SmartScreen or App Control
   warning exactly; unsigned builds are not expected to pass those production
   trust checks.

## Linux

1. Download and verify the same source commit.
2. Run `chmod +x install.sh && ./install.sh` without `sudo`.
3. Confirm **Xendfile** appears in the application menu and, where supported,
   the tray or StatusNotifier area.
4. Run `xendfile --version`, `xendfile peers`, and the systemd commands listed
   in `README.md`.
5. Pair with the Windows test machine, complete the bidirectional and declined
   transfer checks above, then compare the accepted file hashes.
6. Run `./install.sh` again and confirm pairing survives the upgrade with only
   one core and one tray process.
7. Run `./uninstall.sh`, confirm application/startup files are removed, and
   verify received files remain. Reinstall and repeat one transfer.
8. Record the desktop environment, Wayland/X11 session, whether a tray icon was
   visible, and any application-menu or autostart problem.

## Mixed-platform completion evidence

Phase 1's Windows↔Linux line may be checked only after both tester records are
complete and the pair has passed discovery, manual-address fallback, mutual
pairing, send, receive, decline, stop/start, upgrade, uninstall, and reinstall.
macOS↔Windows and macOS↔Linux remain separate required test lines.
