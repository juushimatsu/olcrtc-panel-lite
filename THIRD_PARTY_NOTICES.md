# Third-party notices

- `github.com/openlibrecommunity/olcrtc` - официальный runtime, собираемый без патчей; лицензия upstream: WTFPL.
- `modernc.org/sqlite` и transitive modules - pure-Go SQLite implementation; сохраняются upstream license notices.
- `golang.org/x/crypto` - Go Authors BSD-style license.
- `golang.org/x/sys` - Go Authors BSD-style license.
- `gopkg.in/yaml.v3` - Apache-2.0/MIT notices согласно upstream package.
- `rsc.io/qr` - Go Authors BSD-style license.
- Playwright, Chromium, noVNC, websockify, Xvfb, openbox и x11vnc устанавливаются только опционально на VPS и сохраняют собственные лицензии.

CI формирует полный module inventory командой `go list -m -json all` для каждого release bundle.
