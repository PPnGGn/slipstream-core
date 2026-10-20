# Rebuilds the gomobile bindings. There is now one bound package per OS
# (./android, ./ios) instead of a single shared ./mobile, because each
# platform's tunnel entry differs: Android drives tun2socks from a real TUN fd,
# while iOS has no fd and bridges NEPacketTunnelFlow through an
# io.ReadWriter (see core/tunnel.go). ./desktop is a stub, not bound.
#
# Requires: Go, `gomobile init` (golang.org/x/mobile/cmd/gomobile + gobind, see
# go.mod's `tool` directive), Android NDK for android, Xcode for ios.
#
# LDFLAGS: xray-core's NAT-traversal stack pulls github.com/wlynxg/anet, which
# uses //go:linkname against net.zoneCache. Go >= 1.23 rejects that at link time
# ("invalid reference to net.zoneCache") unless -checklinkname=0 is passed. The
# anet README documents this exact flag; no fork/replace needed.

AAR_OUT := ../slipstream/android/app/libs/slipstreamcore.aar
IOS_XCFRAMEWORK_OUT := slipstreamcore-ios.xcframework
LDFLAGS := -checklinkname=0

.PHONY: bind bind-android bind-ios build vet tidy check-xray-pin

bind: bind-android bind-ios

bind-android:
	gomobile bind -ldflags="$(LDFLAGS)" -target=android -androidapi 21 -javapkg com.slipstream -o $(AAR_OUT) ./android

# iOS device + simulator slices.
# Works around a gomobile bug: the Info.plist it writes into each slice's
# .framework claims MinimumOSVersion=100.0 (not a real iOS version). Swift's
# `import` silently treats that as "incompatible with my deployment target"
# and reports "no such module" with no hint why, so patch it back to our
# actual deployment target (15.0, matches ios/Runner's IPHONEOS_DEPLOYMENT_TARGET).
bind-ios:
	gomobile bind -ldflags="$(LDFLAGS)" -target=ios -o $(IOS_XCFRAMEWORK_OUT) ./ios
	@for plist in $(IOS_XCFRAMEWORK_OUT)/*/*.framework/Info.plist; do \
		plutil -replace MinimumOSVersion -string "15.0" "$$plist"; \
	done

build:
	go build ./...

vet:
	go vet ./...

tidy:
	go mod tidy

# The trimmed geosite.dat / geoip.dat that slipstream-rules publishes are only
# guaranteed to load in the xray-core version *this* module builds against.
# If the two go.mod files drift, slipstream-rules' geocheck still passes (it
# validates against its own pin) while the app hits a parse error at connect
# time. This asserts the pins match; run it in CI / before a release.
# Expects the slipstream-rules repo checked out next to this one; the local
# folder is named slipstream-georules, override with RULES_DIR=... if yours differs.
RULES_DIR ?= ../slipstream-georules
check-xray-pin:
	@here=$$(grep -E '^\s+github.com/xtls/xray-core ' go.mod | awk '{print $$2}'); \
	there=$$(grep -E '^\s+github.com/xtls/xray-core ' $(RULES_DIR)/go.mod | awk '{print $$2}'); \
	if [ -z "$$here" ] || [ -z "$$there" ]; then \
		echo "check-xray-pin: could not read the xray-core pin from one of the go.mod files"; exit 1; \
	fi; \
	if [ "$$here" != "$$there" ]; then \
		echo "check-xray-pin: MISMATCH"; \
		echo "  slipstream-core : $$here"; \
		echo "  slipstream-rules: $$there"; \
		echo "bump both, or the trimmed .dat may not parse in the app."; exit 1; \
	fi; \
	echo "check-xray-pin: ok ($$here)"
