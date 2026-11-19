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

## Tests (qué validan y reglas)
- `main_test.go`: API (403/200 con token), UI/CSP, perfiles sin contraseña en disco, formato servicio/cuenta del llavero (mock), invariantes de código (solo lectura, solo 127.0.0.1, sin recursos externos en `ui.html`) y compilación sin CGO para los 3 destinos.
- `integration_test.go` + `slapd_test.go` + `testdata/seed.ldif`: contra un OpenLDAP real efímero (un único slapd compartido; la app es solo lectura). Incluye TLS/StartTLS, paginación (usuarios con `limits` en slapd.conf que solo ven >100 resultados paginando) y size limit del servidor.
- `keyring_real_test.go`: llavero real; solo con `LDAPNAV_TEST_REAL_KEYRING=1` y en proceso aparte (`-run TestRealKeyring`), porque los demás tests usan `keyring.MockInit`, irreversible.
- `hack/ci-test.sh` es lo que valida un PR; `hack/Dockerfile.test` lo ejecuta en un entorno reproducible (`make test-docker`); `hack/smoke.sh <binario>` prueba un binario compilado.
- La CI (`.github/workflows/ci.yml`) usa `LDAPNAV_REQUIRE_SLAPD=1`: sin slapd falla en vez de saltarse. No añadir tests que se salten en silencio.
- Todo cambio de comportamiento lleva test. `main()` solo cablea; la lógica HTTP está en `newHandler` para poder testearla con `httptest`.
- Los tests no deben subir dependencias al binario más allá de las ya justificadas; el servidor LDAP de pruebas es el `slapd` del sistema, no una librería Go.

## Comandos
- `go build -o ldapnav .` y `./ldapnav` (o `make build` / `make app` en macOS).
- Windows: `go build -o ldapnav.exe .`
- `go vet ./...` y `make test` (o `make test-docker`) antes de dar algo por bueno.
