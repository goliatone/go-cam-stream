#!/bin/bash
# Invoked as root on the selected Pi by provision-camstream.
set -euo pipefail
umask 077

fail() { printf 'provision-camstream: %s\n' "$*" >&2; exit 1; }

stage=$1
release=$2
expected_host=$3
[[ $EUID == 0 ]] || fail "the remote installer requires root"
[[ $release =~ ^camstream-[A-Za-z0-9][A-Za-z0-9._-]*-linux-arm64$ ]] || fail "invalid release name"
[[ $(tr -d '\000' </proc/device-tree/model) == Raspberry\ Pi* ]] || fail "target is not a Raspberry Pi"
if [[ -n $expected_host && $(hostname -s | tr '[:upper:]' '[:lower:]') != "$expected_host" ]]; then
    fail "the target hostname does not match $expected_host"
fi
[[ $(dpkg --print-architecture) == arm64 ]] || fail "target needs ARM64 userspace"
[[ $(stat -fc '%T' /sys/fs/cgroup) == cgroup2fs ]] || fail "target needs unified cgroup v2"
[[ $(</proc/1/comm) == systemd ]] || fail "target must be booted with systemd as PID 1"
manager_version=$(systemctl --system show --property=Version --value) ||
    fail "could not contact the running systemd system manager"
systemd_version=${manager_version%%[!0-9]*}
if [[ ! $systemd_version =~ ^[0-9]+$ ]] || (( systemd_version < 252 )); then
    fail "target needs systemd 252 or newer"
fi
dpkg --compare-versions "$(getconf GNU_LIBC_VERSION | cut -d ' ' -f 2)" ge 2.36 ||
    fail "target needs glibc 2.36 or newer"

exec 9>/run/lock/camstream-provision.lock
flock -n 9 || fail "another provisioning process owns this host"
(cd "$stage" && sha256sum --check --status release.sha256) || fail "uploaded archive checksum mismatch"
if ! systemctl is-active --quiet camstream.service && [[ -n $(ss -H -ltn 'sport = :8080') ]]; then
    fail "port 8080 is already in use outside camstream.service"
fi

root=/opt/camstream
etc=/etc/camstream
units=/etc/systemd/system
service=camstream.service
firewall=camstream-media.service
boot_link="$units/multi-user.target.wants/$service"
firewall_link="$units/$service.requires/$firewall"
target="$root/releases/$release"
install -d -m 0755 "$root" "$root/releases"
[[ ! -e $root/current || -L $root/current ]] || fail "$root/current must be a symlink"
work=$(mktemp -d "$root/.provision.XXXXXX")
was_active=false
activating=false
paths=("$etc/config.yaml" "$etc/secrets.env" "$units/$service" "$root/current" "$boot_link" "$firewall_link")

restore_installation() {
    local path backup
    systemctl stop "$service" || return 1
    for path in "${paths[@]}"; do
        backup="$work/prior$path"
        if [[ -e $backup || -L $backup ]]; then
            cp -aT --remove-destination -- "$backup" "$path" || return 1
        else
            rm -f -- "$path" || return 1
        fi
    done
    systemctl daemon-reload || return 1
    if $was_active; then
        systemctl reset-failed "$service" || return 1
        systemctl start "$service" || return 1
    fi
}

cleanup() {
    local status=$?
    trap - EXIT
    if (( status != 0 )) && $activating; then
        printf 'Camstream activation failed. Restoring the previous installation.\n' >&2
        if ! restore_installation; then
            printf 'Rollback failed. Private backup retained at %s/prior. Check the service journal before retrying.\n' "$work" >&2
            exit "$status"
        fi
    fi
    rm -rf -- "$work"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP

# The archive and its independently obtained checksum are trusted release inputs.
tar -xzf "$stage/release.tar.gz" --no-same-owner --no-same-permissions -C "$work"
candidate="$work/$release"
[[ -d $candidate && ! -L $candidate ]] || fail "archive is missing its release directory"
(cd "$candidate" && sha256sum --check --status SHA256SUMS) || fail "release contents failed checksum verification"
if [[ -e $target || -L $target ]]; then
    if [[ ! -d $target || -L $target ]] ||
        ! cmp -s "$candidate/SHA256SUMS" "$target/SHA256SUMS" ||
        ! (cd "$target" && sha256sum --check --status SHA256SUMS); then
        fail "installed release differs from this archive. Use a new release version."
    fi
else
    # The service account must be able to traverse and execute the release.
    chmod a+rX "$candidate" "$candidate/bin" "$candidate/bin/"*
    mv -T "$candidate" "$target"
fi

packages=()
for package in ca-certificates curl jq nftables python3-ruamel.yaml libgcc-s1; do
    if [[ $(dpkg-query -W -f='${Status}' "$package" 2>/dev/null || true) != "install ok installed" ]]; then
        packages+=("$package")
    fi
done
export DEBIAN_FRONTEND=noninteractive
if (( ${#packages[@]} > 0 )); then
    apt-get update -qq
    apt-get install -y --no-install-recommends "${packages[@]}"
fi
if ! command -v tailscale >/dev/null; then
    # shellcheck disable=SC1091
    . /etc/os-release
    case "${VERSION_CODENAME:-}" in
        bookworm|trixie) ;;
        *) fail "install Tailscale for this distribution before provisioning" ;;
    esac
    repository=https://pkgs.tailscale.com/stable/debian
    curl -fsSL --retry 2 --max-time 30 "$repository/$VERSION_CODENAME.noarmor.gpg" -o "$work/tailscale.gpg"
    curl -fsSL --retry 2 --max-time 30 "$repository/$VERSION_CODENAME.tailscale-keyring.list" -o "$work/tailscale.list"
    install -m 0644 "$work/tailscale.gpg" /usr/share/keyrings/tailscale-archive-keyring.gpg
    install -m 0644 "$work/tailscale.list" /etc/apt/sources.list.d/tailscale.list
    apt-get update -qq
    apt-get install -y --no-install-recommends tailscale
fi
systemctl enable --now tailscaled.service
tailscale_state=$(tailscale status --json || true)
case "$(jq -r '.BackendState // ""' <<<"$tailscale_state")" in
    Running) ;;
    NeedsLogin)
        [[ -s $stage/tailscale.key ]] || fail "join Tailscale first or pass --tailscale-key-file"
        # Suppress enrollment output because an unsuccessful login can print a URL.
        tailscale up --timeout=60s --auth-key="file:$stage/tailscale.key" >/dev/null ||
            fail "Tailscale enrollment failed. Check the auth key and tailnet approval."
        ;;
    Stopped) tailscale up --timeout=60s ;;
    *) fail "Tailscale is not ready. Check tailscale status and device approval." ;;
esac
tail_host=$(tailscale status --json | jq -er 'select(.BackendState == "Running") | .Self.DNSName | rtrimstr(".")')
tail_ip=$(tailscale ip -4)
origin="https://$tail_host"

# Preserve site-owned YAML fields while setting the native deployment topology.
python3 - "$stage/site.yaml" "$work/config.yaml" "$etc/config.yaml" "$tail_host" "$tail_ip" <<'PY'
from ipaddress import IPv4Address
from pathlib import Path
import re
import sys
from ruamel.yaml import YAML, YAMLError

source, output, previous, host, ip = sys.argv[1:]
yaml = YAML(typ="safe", pure=True)
yaml.default_flow_style = False
try:
    address = str(IPv4Address(ip))
    if not re.fullmatch(r"(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+ts\.net", host):
        raise ValueError("Tailscale did not report a Serve hostname")
    config = yaml.load(Path(source))
    if not isinstance(config, dict) or not isinstance(config.get("installation_id"), str):
        raise ValueError("site YAML needs an installation_id")
    if Path(previous).is_file():
        installed = yaml.load(Path(previous))
        if installed.get("installation_id") != config["installation_id"]:
            raise ValueError("site installation_id differs from the installed site")
    backend = config.setdefault("backend", {})
    if not isinstance(backend, dict):
        raise ValueError("site backend must be a mapping")
    media_ports = {"first": 35100, "last": 35103}
    if backend.get("media_ports", media_ports) != media_ports:
        raise ValueError("native provisioning uses media ports 35100–35103")
    config.update(origin=f"https://{host}", listen="127.0.0.1:8080")
    backend.update(
        executable="/opt/camstream/current/bin/go2rtc",
        runtime_dir="/var/lib/camstream/runtime",
        media_address=address,
        candidate_address=address,
        media_ports=media_ports,
    )
    yaml.dump(config, Path(output))
except YAMLError:
    sys.exit("provision-camstream: invalid site YAML or duplicate mapping key")
except (AttributeError, TypeError):
    sys.exit("provision-camstream: invalid site configuration")
except (OSError, ValueError) as error:
    sys.exit(f"provision-camstream: {error}")
PY
"$target/bin/camstream" config validate --config "$work/config.yaml"

serve_address="$tail_host:443"
serve_target=http://127.0.0.1:8080
proxy=$(tailscale serve status --json | jq -er --arg address "$serve_address" --arg target "$serve_target" '
    if .AllowFunnel[$address] == true then "public"
    elif .Web[$address].Handlers["/"].Proxy == $target and .TCP["443"].HTTPS == true then "configured"
    elif .Web[$address].Handlers["/"] != null or (.TCP["443"] != null and .TCP["443"].HTTPS != true) then "occupied"
    else "missing" end
')
case "$proxy" in
    configured|missing) ;;
    public) fail "Funnel is enabled on the dashboard address. Disable it before proceeding." ;;
    *) fail "the Tailscale HTTPS root already serves another application" ;;
esac

getent group camstream >/dev/null || groupadd --system camstream
getent passwd camstream >/dev/null ||
    useradd --system --gid camstream --home-dir /var/lib/camstream --shell /usr/sbin/nologin camstream
[[ $(id -gn camstream) == camstream ]] || fail "camstream needs the camstream primary group"
install -d -m 0750 -o root -g camstream "$etc"
install -d -m 0700 -o camstream -g camstream /var/lib/camstream
secrets="$stage/secrets.env"
if [[ ! -f $secrets ]]; then
    secrets="$work/secrets.env"
    if [[ -f $etc/secrets.env ]]; then
        cp "$etc/secrets.env" "$secrets"
    else
        touch "$secrets"
    fi
fi

# Replace only Camstream's named table in an atomic nftables transaction.
{
    printf 'add table inet camstream_media\ndelete table inet camstream_media\n'
    cat "$target/deploy/media.nft"
} > "$work/media.nft"
cat > "$work/$firewall" <<'UNIT'
[Unit]
Description=Camstream private media firewall
After=nftables.service
Before=camstream.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/camstream/media.nft

[Install]
RequiredBy=camstream.service
UNIT
nft -c -f "$work/media.nft"
if ! cmp -s "$work/media.nft" "$etc/media.nft" || ! cmp -s "$work/$firewall" "$units/$firewall"; then
    install -m 0644 "$work/media.nft" "$etc/media.nft"
    install -m 0644 "$work/$firewall" "$units/$firewall"
fi

unit="$target/deploy/systemd/$service"
if systemctl is-active --quiet "$service"; then
    was_active=true
fi
if [[ $(readlink "$root/current" || true) != "releases/$release" &&
      $(readlink -f "$root/current" || true) != "$target" ]] ||
    ! cmp -s "$work/config.yaml" "$etc/config.yaml" ||
    ! cmp -s "$secrets" "$etc/secrets.env" ||
    ! cmp -s "$unit" "$units/$service"; then
    mkdir -m 0700 "$work/prior"
    for path in "${paths[@]}"; do
        if [[ -e $path || -L $path ]]; then
            cp -a --parents -- "$path" "$work/prior/"
        fi
    done
    activating=true
    if $was_active; then
        systemctl stop "$service"
    fi
    install -m 0640 -g camstream "$work/config.yaml" "$etc/config.yaml"
    install -m 0600 "$secrets" "$etc/secrets.env"
    install -m 0644 "$unit" "$units/$service"
    ln -s "releases/$release" "$work/current"
    mv -Tf "$work/current" "$root/current"
fi

# Enable the firewall dependency after both unit files exist. Unlike a bare
# is-enabled check, the link confirms the relationship survives a reboot.
if [[ ! -L $firewall_link || $(readlink -f "$firewall_link") != "$units/$firewall" ]]; then
    systemctl --system enable "$firewall"
fi
[[ -L $firewall_link && $(readlink -f "$firewall_link") == "$units/$firewall" ]] ||
    fail "systemd did not persist the media firewall dependency"
systemctl --system daemon-reload
unit_properties=$(systemctl --system show "$service" --property=LoadState,FragmentPath,Restart,Requires,After)
for expected in "LoadState=loaded" "FragmentPath=$units/$service" "Restart=on-failure"; do
    grep -Fxq "$expected" <<<"$unit_properties" || fail "systemd did not load $expected for $service"
done
for dependency in Requires After; do
    if ! grep -Eq "^$dependency=(.* )?camstream-media\\.service( |$)" <<<"$unit_properties"; then
        fail "systemd did not apply the media firewall $dependency dependency"
    fi
done
if systemctl --system is-active --quiet "$firewall"; then
    # Reapply the Camstream table without stopping the running server.
    nft -f "$etc/media.nft"
else
    systemctl --system start "$firewall"
fi
if [[ $proxy == missing ]]; then
    timeout 45s tailscale serve --bg --https=443 "$serve_target"
fi
if $activating || systemctl --system is-failed --quiet "$service"; then
    systemctl --system reset-failed "$service"
fi
systemctl start "$service"
deadline=$((SECONDS + 30))
until curl --noproxy '*' --silent --fail --max-time 2 "$serve_target/readyz" >/dev/null; do
    (( SECONDS < deadline )) || fail "readiness timed out. Check journalctl -u $service"
    sleep 1
done
tailscale serve status --json | jq -e --arg address "$serve_address" --arg target "$serve_target" \
    '.Web[$address].Handlers["/"].Proxy == $target and .TCP["443"].HTTPS == true and .AllowFunnel[$address] != true' >/dev/null ||
    fail "could not verify private Tailscale Serve configuration"
if [[ ! -L $boot_link || $(readlink -f "$boot_link") != "$units/$service" ]]; then
    systemctl --system enable "$service"
fi
[[ -L $boot_link && $(readlink -f "$boot_link") == "$units/$service" ]] &&
    [[ $(systemctl --system is-enabled "$service") == enabled ]] ||
    fail "systemd did not persist the $service boot link"
systemctl --system is-active --quiet "$service" || fail "$service is not active"
activating=false
printf 'Ready: %s\nService: %s (active, enabled, restart on failure)\nDashboard: %s\n' "$release" "$service" "$origin"
