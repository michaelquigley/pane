.PHONY: frontend build dev clean

frontend:
	cd ui && npm install && npm run build

build: frontend
	go build -o build/pane ./cmd/pane

dev:
	cd ui && npm run dev &
	go run ./cmd/pane --config ./pane.yaml

clean:
	rm -rf build/ ui/dist/
