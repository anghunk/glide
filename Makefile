.PHONY: dev

dev:
ifeq ($(shell uname -s),Darwin)
	python3 scripts/macos-signing.py prepare
	go tool mygo dev -sign "$$(python3 scripts/macos-signing.py identity)"
else
	go tool mygo dev
endif
