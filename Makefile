.PHONY: frontend build clean

frontend:
	cd ui && npm install && npm run build

build: frontend

clean:
	rm -rf ui/dist/ ui/node_modules/
