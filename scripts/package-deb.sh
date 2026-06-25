#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
if [[ -z "$version" ]]; then
  echo "usage: $0 VERSION" >&2
  exit 2
fi

package_root="dist/deb/proxemby"
install -d \
  "$package_root/DEBIAN" \
  "$package_root/usr/bin" \
  "$package_root/etc/proxemby" \
  "$package_root/lib/systemd/system" \
  "$package_root/usr/share/doc/proxemby"

install -m 0755 dist/proxemby-linux-amd64/proxemby "$package_root/usr/bin/proxemby"
install -m 0644 examples/proxemby.toml "$package_root/etc/proxemby/proxemby.toml"
install -m 0644 packaging/proxemby.service "$package_root/lib/systemd/system/proxemby.service"
install -m 0644 README.md "$package_root/usr/share/doc/proxemby/README.md"

cat >"$package_root/DEBIAN/control" <<EOF
Package: proxemby
Version: $version
Section: net
Priority: optional
Architecture: amd64
Depends: ca-certificates
Maintainer: proxemby maintainers
Description: Host-based Emby edge proxy
 proxemby proxies Emby client traffic to upstream Emby servers and
 rewrites PlaybackInfo resource URLs so media can flow through proxemby.
EOF

cat >"$package_root/DEBIAN/conffiles" <<EOF
/etc/proxemby/proxemby.toml
EOF

dpkg-deb --build --root-owner-group "$package_root" "dist/proxemby_${version}_amd64.deb"
