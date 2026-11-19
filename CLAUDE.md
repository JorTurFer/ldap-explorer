# ldapnav

Navegador LDAP mínimo y de solo lectura. Es un binario Go único que sirve una UI web embebida (`ui.html`, vía `embed`) en 127.0.0.1 y abre el navegador.

## Estructura
- `main.go`: servidor HTTP, API JSON bajo `/api/*`, cliente LDAP (github.com/go-ldap/ldap/v3) y llavero (github.com/zalando/go-keyring).
- `ui.html`: UI en HTML/CSS/JS vanilla, sin build ni dependencias.
- `Makefile` + `Info.plist`: empaquetado como `.app` en macOS.

## Invariantes (no romper)
- Solo lectura: nada de Add/Modify/Delete contra el LDAP salvo que se pida explícitamente.
- Solo escucha en 127.0.0.1. Toda la API exige la cabecera `X-Token`; el token va en el `#` de la URL.
- Nunca guardar contraseñas en `profiles.json`: solo en el llavero del sistema (`go-keyring`), servicio `ldapnav`, cuenta `bindDN@url`. No cambiar servicio ni cuenta: rompería las contraseñas ya guardadas.
- Sin CDNs ni recursos externos en la UI: la CSP lo bloquea a propósito.
- Debe compilar con `CGO_ENABLED=0` para darwin/arm64, darwin/amd64 y windows/amd64.

## Pendiente / ideas
- Empaquetado en Windows (.exe con icono, o sin consola con `-ldflags -H=windowsgui`).
- Llavero: `go-keyring` en macOS sigue llamando a `/usr/bin/security` internamente y guarda con prefijo `go-keyring-base64:`; lee bien las contraseñas antiguas en texto plano. Sin probar en Windows/macOS reales.
- hasSubordinates no existe en AD, así que todos los nodos muestran ▶.

## Comandos
- `go build -o ldapnav .` y `./ldapnav` (o `make build` / `make app` en macOS).
- Windows: `go build -o ldapnav.exe .`
- `go vet ./...` antes de dar algo por bueno.
