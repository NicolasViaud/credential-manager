.PHONY: all wcm proxy ui ui-dev clean

all: ui wcm

## Build the TypeScript UI (outputs bundle.js into wcm/cmd/wcm/static/)
ui:
	cd wcm/ui-src && npm install && npm run build

## Watch mode — recompiles UI on every file change (for development)
ui-dev:
	cd wcm/ui-src && npm install && npm run dev

## Build the WCM server binary (requires UI to be built first)
wcm: ui
	go build -o bin/wcm ./wcm/cmd/wcm/

## Build the D-Bus proxy binary (Linux only)
proxy:
	go build -o bin/proxy ./proxy/cmd/proxy/

clean:
	rm -rf bin/ wcm/cmd/wcm/static/bundle.js wcm/ui-src/node_modules
