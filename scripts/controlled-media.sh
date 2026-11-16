#!/bin/sh
# Disposable local Linux arm64 validation. No host caches, services or cameras.
set -eu
usage() {
    echo 'Usage: ./scripts/controlled-media.sh [--fresh] [--management-probes|--management-host] [--output NEW_DIRECTORY]'
    echo 'Requires Docker (Linux arm64 execution), tar, and network access for build dependencies.'
    echo '--fresh disables Docker build-layer reuse; evidence is retained, runtime resources are removed.'
}
fresh=false
test_name=TestT05ProductionRTSP
output=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --fresh) fresh=true; shift ;;
        --management-probes) test_name=TestManagementProbes; shift ;;
        --management-host) test_name=TestManagementHost; shift ;;
        --output) [ "$#" -ge 2 ] || { usage >&2; exit 2; }; output=$2; shift 2 ;;
        --help|-h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
command -v docker >/dev/null || { echo 'Docker is required' >&2; exit 1; }
docker info >/dev/null
repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
if [ -n "$output" ]; then
    mkdir -p "$(dirname -- "$output")"
    mkdir -- "$output" || { echo 'Output directory must be new' >&2; exit 1; }
else
    output=$(mktemp -d "${TMPDIR:-/tmp}/camstream-media-evidence.XXXXXXXX")
fi
output=$(CDPATH='' cd -- "$output" && pwd)
work=$(mktemp -d "${TMPDIR:-/tmp}/camstream-media.XXXXXXXX")
run=$(basename "$work" | tr '[:upper:]' '[:lower:]' | tr '.' '-')
label=io.camstream.controlled-media.run
origin="$run-origin"
app="$run-app"
image="$run:test"
child=
result=FAIL

cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    # A foreground Docker wait/build runs asynchronously so signals reach this trap.
    if [ -n "$child" ]; then
        kill "$child" 2>/dev/null || :
        wait "$child" 2>/dev/null || :
    fi
    clean=true
    if docker info >/dev/null 2>&1; then
        for name in "$app" "$origin"; do
            if docker container inspect "$name" >/dev/null 2>&1; then
                owner=$(docker container inspect --format "{{index .Config.Labels \"$label\"}}" "$name") || owner=
                if [ "$owner" = "$run" ]; then
                    docker logs "$name" > "$output/$name.log" 2>&1 || :
                    docker rm -f "$name" >> "$output/cleanup.log" 2>&1 || clean=false
                else
                    echo "Refusing to remove unowned container $name" >> "$output/cleanup.log"
                    clean=false
                fi
            fi
        done
        if docker image inspect "$image" >/dev/null 2>&1; then
            owner=$(docker image inspect --format "{{index .Config.Labels \"$label\"}}" "$image") || owner=
            if [ "$owner" = "$run" ]; then
                docker image rm "$image" >> "$output/cleanup.log" 2>&1 || clean=false
            else
                echo "Refusing to remove unowned image $image" >> "$output/cleanup.log"
                clean=false
            fi
        fi
        remaining=$(docker ps -aq --filter "label=$label=$run") || clean=false
        [ -z "$remaining" ] || clean=false
        for name in "$app" "$origin"; do
            if docker container inspect "$name" >/dev/null 2>&1; then clean=false; fi
        done
        if docker image inspect "$image" >/dev/null 2>&1; then clean=false; fi
    else
        echo 'Docker unavailable: cleanup could not be verified' >> "$output/cleanup.log"
        clean=false
    fi
    if [ "$clean" = true ]; then
        rm -rf "$work" || clean=false
    fi
    if [ "$clean" != true ]; then
        echo "Cleanup needs attention: $app $origin $image; scratch retained: $work" >&2
        status=1
    fi
    if [ "$status" -ne 0 ]; then result=FAIL; fi
    printf 'result=%s\nexit_status=%s\ncleanup=%s\nrun=%s\n' "$result" "$status" "$clean" "$run" > "$output/result.txt"
    echo "Controlled media: $result; cleanup=$clean; evidence=$output"
    exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
: > "$output/cleanup.log"
printf 'run=%s\norigin=%s\napp=%s\nimage=%s\nscratch=%s\nfresh=%s\n' "$run" "$origin" "$app" "$image" "$work" "$fresh" > "$output/run.txt"
echo "Evidence: $output"

# Copy only maintained build inputs. Exclude generated assets/dependency trees,
# VCS metadata and local state; no .ctx, environment files or host caches.
mkdir "$work/source"
tar -C "$repo" --exclude=node_modules --exclude=dist --exclude=.git \
    --exclude=data/web --exclude=.web-staging --exclude=.DS_Store --exclude=.env --exclude='.env.*' \
    -cf "$work/source.tar" go.mod go.sum .golangci.yml .golangci-lint-version \
    cmd config deploy data frontend internal pkg scripts/controlled-media
tar -C "$work/source" -xf "$work/source.tar"
set -- docker build --platform linux/arm64 --progress plain --label "$label=$run" \
    -f "$work/source/scripts/controlled-media/Dockerfile" -t "$image"
if [ "$fresh" = true ]; then set -- "$@" --no-cache; fi
echo 'Building tools, embedded assets, fixture media and test binary…'
"$@" "$work/source" > "$output/build.log" 2>&1 &
child=$!
wait "$child" || { child=; echo "Build failed; see $output/build.log" >&2; exit 1; }
child=
docker image inspect --format '{{.Id}}' "$image" > "$output/image.txt"

# Separate PID namespaces: fixture FFmpeg never runs among application workers.
# Share only loopback networking; nothing is published or externally reachable.
docker create --init --name "$origin" --label "$label=$run" --network none \
    --cpus 2 --memory 512m --entrypoint /bin/sh "$image" \
    -c 'go2rtc -config /fixture/other.yaml & exec go2rtc -config /fixture/origin.yaml' > /dev/null
docker cp "$origin:/fixture/versions.txt" "$output/versions.txt"
docker start "$origin" > /dev/null
attempt=0
until docker exec "$origin" sh -c 'curl --fail --silent http://127.0.0.1:34000/api/streams >/dev/null && curl --fail --silent http://127.0.0.1:34002/api/streams >/dev/null'; do
    attempt=$((attempt + 1))
    [ "$attempt" -lt 20 ] || { echo 'Fixture origin did not become ready' >&2; exit 1; }
    sleep 1
done
docker create --init --name "$app" --label "$label=$run" \
    --network "container:$origin" --cpus 2 --memory 768m \
    -e CAMSTREAM_RTSP_T05=1 "$image" \
    -test.run "^${test_name}$" -test.v -test.timeout 90s > /dev/null
docker start "$app" > /dev/null
echo 'Running production RTSP/API media checks…'
docker wait "$app" > "$output/container-exit.txt" &
child=$!
wait "$child" || { child=; exit 1; }
child=
docker logs "$app" > "$output/api.log" 2>&1
cat "$output/api.log"
[ "$(cat "$output/container-exit.txt")" = 0 ] || exit 1
grep -q "^--- PASS: ${test_name} " "$output/api.log" || { echo 'Required live test did not pass' >&2; exit 1; }
result=PASS
