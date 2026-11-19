APP := LDAPNav.app

.PHONY: build app clean

build:
	go mod tidy
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ldapnav .

# Empaqueta como .app para lanzarlo desde Finder/Spotlight/Dock.
app: build
	rm -rf $(APP)
	mkdir -p $(APP)/Contents/MacOS
	cp ldapnav $(APP)/Contents/MacOS/ldapnav
	cp Info.plist $(APP)/Contents/Info.plist
	codesign --force --sign - $(APP)
	@echo "Listo: arrastra $(APP) a /Applications"

clean:
	rm -rf ldapnav $(APP)
