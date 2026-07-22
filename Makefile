# Rebuilds the gomobile bindings. There is now one bound package per OS
# (./android, ./ios, ./mac) instead of a single shared ./mobile, because each
# platform's tunnel entry differs: Android drives tun2socks from a real TUN fd,
# while iOS/macOS have no fd and bridge NEPacketTunnelFlow through an
# io.ReadWriter (see core/tunnel.go). ./desktop is a stub, not bound.
#
# Requires: Go, `gomobile init` (golang.org/x/mobile/cmd/gomobile + gobind, see
# go.mod's `tool` directive), Android NDK for android, Xcode for ios/mac.

AAR_OUT := ../v2net/android/app/libs/v2netcore.aar
IOS_XCFRAMEWORK_OUT := v2netcore-ios.xcframework
MAC_XCFRAMEWORK_OUT := v2netcore-mac.xcframework

.PHONY: bind bind-android bind-ios bind-mac build vet tidy

bind: bind-android bind-ios bind-mac

bind-android:
	gomobile bind -target=android -androidapi 21 -javapkg com.v2net -o $(AAR_OUT) ./android

# iOS device + simulator slices.
# Works around a gomobile bug: the Info.plist it writes into each slice's
# .framework claims MinimumOSVersion=100.0 (not a real iOS version). Swift's
# `import` silently treats that as "incompatible with my deployment target"
# and reports "no such module" with no hint why, so patch it back to our
# actual deployment target (15.0, matches ios/Runner's IPHONEOS_DEPLOYMENT_TARGET).
bind-ios:
	gomobile bind -target=ios -o $(IOS_XCFRAMEWORK_OUT) ./ios
	@for plist in $(IOS_XCFRAMEWORK_OUT)/*/*.framework/Info.plist; do \
		plutil -replace MinimumOSVersion -string "15.0" "$$plist"; \
	done

# macOS slice. gomobile emits a macos-arm64(_x86_64) slice for -target=macos.
# Same MinimumOSVersion=100.0 bug as bind-ios (see comment above); macOS
# .frameworks are versioned bundles, so the Info.plist lives one level deeper
# under Versions/A/Resources.
bind-mac:
	gomobile bind -target=macos -o $(MAC_XCFRAMEWORK_OUT) ./mac
	@for plist in $(MAC_XCFRAMEWORK_OUT)/*/*.framework/Versions/A/Resources/Info.plist; do \
		plutil -replace MinimumOSVersion -string "13.0" "$$plist"; \
	done

build:
	go build ./...

vet:
	go vet ./...

tidy:
	go mod tidy
