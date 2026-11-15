#!/usr/bin/env bash
# Builds the Linux binary and fnOS .fpk packages on Linux/macOS.
#
# Usage:
#   ./build.sh                 # x86 + arm packages
#   ARCHS="amd64" ./build.sh   # x86 only
#   FNPACK=/usr/local/bin/fnpack ./build.sh
set -euo pipefail

cd "$(dirname "$0")"
ROOT=$(pwd)
DIST="$ROOT/dist"

VERSION=${VERSION:-$(sed -n 's/.*Version = "\([^"]*\)".*/\1/p' internal/version/version.go | head -n 1)}
if [ -z "$VERSION" ]; then
    echo "无法读取版本号" >&2
    exit 1
fi

# The .fpk name and the injected binary version come from $VERSION, while the
# app center reads pack/manifest. Drift here produces a package that installs as
# 1.0.1 but still displays 1.0.0, so fail instead of guessing.
MANIFEST_VERSION=$(sed -n 's/^version[[:space:]]*=[[:space:]]*\([^[:space:]]*\).*/\1/p' pack/manifest | head -n 1)
if [ -z "$MANIFEST_VERSION" ]; then
    echo "pack/manifest 缺少 version 字段" >&2
    exit 1
fi
if [ "$MANIFEST_VERSION" != "$VERSION" ]; then
    echo "版本号不一致：version.go/构建参数为 $VERSION，pack/manifest 为 $MANIFEST_VERSION。请先统一。" >&2
    exit 1
fi

FNPACK=${FNPACK:-$(command -v fnpack || true)}
if [ -z "$FNPACK" ]; then
    echo "未找到 fnpack。请从 https://developer.fnnas.com/docs/cli/fnpack/ 下载，并用 FNPACK 指定路径。" >&2
    exit 1
fi

mkdir -p "$DIST"
for pair in ${ARCHS:-amd64:x86 arm64:arm}; do
    GOARCH_NAME=${pair%%:*}
    PKG_ARCH=${pair##*:}
    STAGE="$DIST/stage-$PKG_ARCH"
    rm -rf "$STAGE"
    cp -R "$ROOT/pack" "$STAGE"

    echo "==> 编译 linux/$GOARCH_NAME"
    CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH_NAME" go build -trimpath \
        -ldflags "-s -w -X fnososs/internal/version.Version=$VERSION" \
        -o "$STAGE/app/fnososs" ./cmd/ossd

    sed -i "s/^platform[[:space:]]*=.*/platform=$PKG_ARCH/" "$STAGE/manifest"
    chmod +x "$STAGE/app/fnososs"

    echo "==> 打包 $PKG_ARCH"
    (cd "$DIST" && "$FNPACK" build --directory "$STAGE")
    mv -f "$DIST/fnos-oss.fpk" "$DIST/fnos-oss_${VERSION}_${PKG_ARCH}.fpk"
    echo "==> 修正包内文件权限"
    go run ./tools/fixfpk "$DIST/fnos-oss_${VERSION}_${PKG_ARCH}.fpk"
    echo "==> $DIST/fnos-oss_${VERSION}_${PKG_ARCH}.fpk"
done

echo "完成。安装包位于 $DIST"
