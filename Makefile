# Rebuilds the gomobile bindings from ./mobile (the only package gomobile
# binds — see mobile/mobile.go for why). Requires: Go, `gomobile init`
# (golang.org/x/mobile/cmd/gomobile + gobind, see go.mod's `tool` directive),
# Android NDK for the android target, Xcode for the ios target.

AAR_OUT := ../v2net/android/app/libs/v2netcore.aar
XCFRAMEWORK_OUT := v2netcore.xcframework

.PHONY: bind-android bind-ios build vet tidy

bind-android:
	gomobile bind -target=android -androidapi 21 -javapkg com.v2net -o $(AAR_OUT) ./mobile

bind-ios:
	gomobile bind -target=ios -o $(XCFRAMEWORK_OUT) ./mobile

build:
	go build ./...

vet:
	go vet ./...

tidy:
	go mod tidy
