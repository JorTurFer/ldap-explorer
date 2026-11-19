# ldapnav

Navegador LDAP mínimo y de solo lectura (macOS y Windows). Es un binario Go que sirve la UI en `127.0.0.1` y abre tu navegador.

- Árbol navegable (doble clic o ▶ para expandir, clic para ver los atributos)
- Búsquedas con base, filtro, ámbito, atributos y límite (paginadas)
- Atributos operacionales opcionales; los binarios se muestran como resumen hex
- Perfiles guardados en `~/Library/Application Support/ldapnav/profiles.json` (sin contraseña)
- Contraseña opcional en el llavero del sistema (Keychain en macOS, Credential Manager en Windows, Secret Service en Linux) vía `go-keyring`; servicio `ldapnav`, cuenta `bindDN@url`
- `ldap://`, `ldaps://` y StartTLS; base DN autodetectada del RootDSE

## Compilar

Requiere Go ≥ 1.24.

```sh
make build        # binario ./ldapnav
./ldapnav         # abre el navegador

make app          # LDAPNav.app (firmado ad-hoc), arrástralo a /Applications
```

Al compilarlo en tu máquina, el binario no lleva el atributo de cuarentena y Gatekeeper no se queja.

## Opciones

```
-port 0            puerto local (0 = aleatorio)
-no-open           no abrir el navegador
-idle-exit 3m      salir si la pestaña lleva este tiempo cerrada (0 = nunca)
```

## Seguridad

- Solo escucha en `127.0.0.1`.
- Cada arranque genera un token aleatorio que va en el `#` de la URL. La API lo exige en una cabecera, así que otras webs abiertas en el navegador no pueden usar el servidor.
- No escribe nada en el LDAP: solo hace bind y search.
