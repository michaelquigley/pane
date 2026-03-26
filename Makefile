.PHONY: frontend build dev clean

frontend:
	cd ui && npm install && npm run build && go install ./...

clean:
	rm -rf ui/dist/ ui/node_modules/
