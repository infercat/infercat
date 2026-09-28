# Infercat for Mac. Unsigned everywhere: no signing identity is involved in a build,
# a test run or a screenshot pass. See README.md.
DERIVED ?= /tmp/infercat-mac
OUT ?= /tmp/infercat-mac-screens
APP = $(DERIVED)/Build/Products/Debug/Infercat.app
XCB = xcodebuild -project InfercatMac.xcodeproj -scheme Infercat -configuration Debug \
	-destination 'platform=macOS' -derivedDataPath $(DERIVED) CODE_SIGNING_ALLOWED=NO

.PHONY: build test screens fixtures clean

build:
	$(XCB) build

test:
	$(XCB) build test

# Every state this build has, offscreen, from fixtures: no host, no data directory.
screens: build
	rm -rf "$(OUT)"
	"$(APP)/Contents/MacOS/Infercat" --capture "$(OUT)"
	@echo "screens: $(OUT)"

# Re-vendor the contract fixtures from the CLI's testdata; CI fails on a diff.
fixtures:
	sh vendor-fixtures.sh

clean:
	rm -rf $(DERIVED)
