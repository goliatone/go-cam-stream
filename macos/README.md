# Native camera app and pinned runtime

## Menu-bar app

Build the local Apple Silicon/macOS 26+ development app from the prepared pack:

```sh
python3 macos/build-app.py
```

Open `dist/Camstream Camera.app` in Finder. Allow camera access, select a webcam,
choose a camera name (stream ID), and save settings. Saving never starts capture.
For sound, turn on **Enable microphone**, choose **Allow Microphone** if needed,
select a microphone, and save. Microphone audio is off by default; permission
grant, selection, Save and reopening never start capture. Denial leaves video-only
use available by disabling microphone audio. Choose **Start Camera** explicitly. The camera icon in the menu bar offers Start/Stop,
Connection Details, Settings and Quit. Closing the window leaves the menu-bar
app running; the Dock entry and app menu also provide access to the controls.
The icon is outlined while stopped and filled while live; its menu and accessibility label report the full capture state.
Quit waits for helper-owned capture cleanup. Reopening starts stopped.

If the camera icon is missing, check **System Settings → Menu Bar → Allow in the
Menu Bar → Camstream Camera**. A crowded menu bar can also hide it behind a
MacBook notch. Temporarily hide another menu-bar item, hold **Command** and drag
the camera icon farther right, then restore the other item. The Dock entry and
**Camstream Camera** app menu remain available while the icon is hidden.
[Apple’s menu-bar rearrangement guide](https://support.apple.com/en-gb/guide/mac-help/mchlp1446/26/mac/26).

**Only this Mac** binds loopback for local testing. For a server on another
computer, choose **Private network** and enter this Mac’s reachable private IP or
hostname. The listener uses all IPv4 interfaces, with authenticated readers; use
a trusted LAN/VPN and keep the port off the public internet. Settings are locked
while capture is starting, live or stopping.

Connection Details shows a credential-free endpoint. **Export Connection Files**
creates mode-0600 YAML and environment files while stopped or live; it does not
stop capture, change settings or rotate credentials. **Show Files in Finder**
reveals existing private files, including after relaunch, without displaying the
password in the app or clipboard. Both access modes require reader credentials.
For video-only web onboarding, keep capture running and choose **Add Camera →
Import Mac files**, select both files, then **Test connection → Save Camera →
Watch Camera**. YAML-only import requires entering the private .env credentials.
For startup YAML deployments, merge the camera/grant and supply environment values
separately. Export does not automatically pair with or restart a server.

The app identity is `com.goliatone.camstream.camera`. Settings and credentials
live in `~/Library/Application Support/camstream-camera-app`, separate from the
CLI and proof. Replacing the app preserves this directory and credentials;
there is no automatic import of prototype settings. Permission is independent of
Camstream Camera Proof. If denied, enable **Camstream Camera** in Privacy &
Security → Camera or Microphone. Follow any macOS **Quit & Reopen** prompt; if the app still
shows the old permission state, quit and reopen it before refreshing devices
and choosing Start explicitly. Changing permission never starts capture. A missing
saved microphone stays unavailable until it is reconnected or explicitly replaced;
duplicate display names have stable distinguishing suffixes. Browser mute changes
playback only. Local preview stays silent even when microphone audio is live.
Errors remain visible until an explicit successful action; polling cannot hide
capture failures. Unexpected helper exit requires quitting/reopening the app.

`build-app.py` shares the proof’s verified offline assembler and accepts the same
`--runtime-pack`, `--cache`, `--download`, `--output` and `--version` flags. The
bundle is ad-hoc hardened and includes the pinned runtime, sources and notices.
The local app is not a published release. Use the release packager below for a
versioned DMG. Earlier video-only Developer ID/notarized app, capture and replacement checks
passed on macOS 26.7.1. The runtime-3 development app passes local assembly,
154 native checks, permission denial/recovery, real mono Opus transport and
WebRTC browser delivery through the validation server. Ten-minute packet continuity
and native cleanup pass. Operator-confirmed audibility, physical A/V sync and final
signed delivery remain T04. Hosted download and oldest-OS validation remain separate gates.

Camera-free app state and transport checks:

```sh
app_check_dir=$(mktemp -d)
xcrun swiftc -swift-version 5 -module-cache-path "$app_check_dir/modules" \
  macos/CameraApp/{CameraState,CameraPermissions,HelperClient,PreviewReader,PreviewController,CameraModel}.swift \
  macos/CameraAppTests/{AudioFixtures,AudioTests,main}.swift -o "$app_check_dir/camera-app-tests"
"$app_check_dir/camera-app-tests" "$PWD/macos/CameraAppTests/fake-helper.py"
rm -rf "$app_check_dir"
```

The native tests use T03's shared audio JSON, fake permission/device providers,
and fixture helpers; none can activate a sensor. Set `CAMSTREAM_NATIVE_HELPER` to
a freshly built helper beside verified runtime tools to add read-only `config` /
`status` integration checks. `CameraAppTests/AudioUI/main.swift` is an isolated
SwiftUI composition for interactive review: it links only in-memory fixtures and
never launches a helper or calls the system permission provider. It is not a
production bundle or an end-to-end capture proof.

`python3 -m unittest discover -s macos -p 'test_native_permissions.py' -v`
checks production microphone usage text/entitlement selection through mocked
assembly, preserving the camera-only proof identity. Production builds select
`CameraApp/entitlements.plist`; no permission grant is requested by packaging.

## Minimum macOS validation

The runtime targets Apple Silicon/macOS 26.0. Testing on 26.7.1 does not certify
26.0. Use an Apple Silicon Mac capable of booting **26.0**, preferably a MacBook
with its built-in camera, and an operator who can answer macOS camera prompts.
A spare Mac or an independently bootable test volume avoids replacing the
working system. Confirm the exact OS version/build before testing; installer
availability and hardware compatibility must be checked first. Apple documents
[separate macOS volumes](https://support.apple.com/en-us/118282) and
[external startup disks](https://support.apple.com/en-gb/111336).

Test the exact signed candidate DMG and record its checksum. Cover normal browser
download/copy/Finder launch and Gatekeeper, stopped launch, permission denial and
recovery, real preview, authenticated local/private-network playback and invalid
reader rejection, status-menu controls, Stop/Quit and app/helper-loss cleanup.
Verify offline bundled-runtime use and settings/permission preservation across
replacement/rollback. Use a second LAN client to certify remote reachability;
connecting to the Mac's own LAN address proves only its local network path.
Record technical results and OS/artifact identifiers, not images or credentials.
A VM without a verified camera/permission path supplies only supplemental launch
coverage. Keep the release unpublished until the required hardware checks pass.

## Versioned releases

The camera DMG and Linux server archive use one explicit `MAJOR.MINOR.PATCH`
version. Runtime dependencies retain their independent versions and locked hashes.
No build command bumps a version, commits, creates a tag, pushes or publishes.

Local development DMG (Apple Silicon Mac with Go/Xcode and the prepared pack):

```sh
python3 macos/package-release.py 0.1.0 --development
python3 macos/package-release.py --verify dist/releases/camstream-camera-0.1.0-darwin-arm64-development
```

This produces a clearly labeled development DMG, app, `release.json` and
`SHA256SUMS` in a new version directory. The DMG contains the app, an Applications
shortcut and installation instructions. Existing output is never overwritten.
The app includes every runtime tool, license/source archive, runtime provenance
and application/runtime version metadata. Settings are never packaged.

For external distribution, use a clean checkout, a **Developer ID Application**
identity in the build Mac's Keychain, and an existing `notarytool` Keychain profile:

```sh
python3 macos/package-release.py 0.1.0 \
  --sign-identity 'Developer ID Application: YOUR NAME (TEAMID)' \
  --notary-profile camstream-notary
python3 macos/package-release.py --verify dist/releases/camstream-camera-0.1.0-darwin-arm64 --require-notarized
```

Do not put certificate passwords, private keys or Apple account credentials in
arguments, Git or documentation. Provision the Keychain/profile separately.
An Apple Development certificate is not a Developer ID distribution identity.
The packager timestamps and signs nested code before the app, submits an app ZIP
for notarization, staples the app, then builds/signs/notarizes/staples the DMG.
Only Accepted submissions, successful ticket validation and Gatekeeper assessments
produce distribution output. This command uploads the app/DMG to Apple's notary
service; it does not publish a GitHub release. Development mode never uses these
credentials. See [Apple's notarization workflow](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution).

Checksums cover the final stapled DMG and release metadata. The metadata records
source commit/dirty state, application/runtime versions, target, notarization IDs
and every final app file's hash/mode. A successful signature does not prove camera
permission behavior: repeat native permission/menu/capture/cleanup validation on
the downloaded signed build and the minimum supported macOS before publication.
The initial target is Apple Silicon/macOS 26.0; local testing on a newer host does
not certify that minimum or Intel compatibility.

### One-time signing setup

1. In Xcode Settings → Accounts (or Apple Accounts), sign in and select the paid
   developer team. Open Manage Certificates, click **+**, and choose **Developer
   ID Application**. Use an existing valid identity if already installed. Apple
   requires the Account Holder role to create this certificate; if the option is
   unavailable, check the selected team/role. This is different from Apple
   Development, Apple Distribution and Developer ID Installer.
2. In Terminal, run `security find-identity -v -p codesigning`. Confirm an entry
   named `Developer ID Application: NAME (TEAMID)` appears. That complete display
   name is the packager's `--sign-identity`; the private key stays in Keychain.
3. In [Apple Account](https://account.apple.com), open Sign-In and Security →
   App-Specific Passwords and generate one labeled **Camstream notarization**.
   This requires two-factor authentication. Keep it private.
4. Find the paid team's Team ID in the developer account's Membership details.
   Store the notarization credentials locally (replace the two placeholders):

   ```sh
   xcrun notarytool store-credentials "camstream-notary" --apple-id "YOUR_APPLE_ID" --team-id "YOUR_TEAM_ID"
   ```

   Paste the app-specific password at the secure prompt. Do not add `--password`
   or put the password in shell history, Git or chat. The command validates the
   credentials and stores them in Keychain under the profile name.
5. Verify access with `xcrun notarytool history --keychain-profile camstream-notary`.
   An empty successful history is fine. Only the certificate display name and
   profile name are needed by the release build.

References: [Apple certificate creation](https://developer.apple.com/help/account/certificates/create-developer-id-certificates/),
[Xcode certificate manager](https://help.apple.com/xcode/mac/current/en.lproj/dev154b28f09.html),
[app-specific passwords](https://support.apple.com/en-us/102654), and
[notarytool Keychain credentials](https://developer.apple.com/documentation/technotes/tn3147-migrating-to-the-latest-notarization-tool).

### Shared camera/server CI

The release repository is `goliatone/go-cam-stream`. The versioned
`.github/workflows/release-artifacts.yml` runs on GitHub-hosted `macos-26` and
`ubuntu-24.04-arm`. The runtime URL defaults to `macos/runtime.lock.json`; an
optional HTTPS mirror override must still match its pinned SHA-256. Runtime
versions are hosted separately from app versions.
[Runtime-1](https://github.com/goliatone/go-cam-stream/releases/tag/camera-runtime-1)
is published for the historical signed 0.1.0/0.1.1 builds; its anonymous download
passes the pinned hash and inventory checks. [Runtime-2](https://github.com/goliatone/go-cam-stream/releases/tag/camera-runtime-2)
is published for historical preview-enabled builds. The current lock selects
the local audio-capable runtime-3 pack, whose URL is unset; supply the locked
archive or preseed the cache. Neither older pack satisfies the current lock.
For each new runtime, prepare a draft, review archive/checksums/provenance and
explicitly authorize publication before expecting anonymous downloads to work.

Use `./taskfile release patch` (or `minor` / `major`) from a clean, reviewed
`main` checkout. It bumps the checked-in `.version`, generates/commits the
changelog and atomically pushes the branch plus annotated version tag. Preview
with `./taskfile release patch --dry-run`; see the [release guide](../README.md#release).
The new `vMAJOR.MINOR.PATCH` tag starts the release workflow automatically.
CI requires the committed `.version` to match the tag; runtime-only tags such as
`camera-runtime-2` do not trigger it. The workflow runs tests, builds the signed and
notarized camera DMG plus Linux server, and creates an unpublished draft. It then
waits at **release-publish** for one approval from `goliatone`.

Before approving, download and validate the draft, including camera permission,
preview and cleanup on the supported macOS targets. Approving **release-publish**
publishes that draft publicly. It rechecks the tag's commit and the draft's asset
IDs, sizes and digests; changed assets or a moved tag fail publication. Reject or
leave the approval pending to keep the release unpublished. The workflow never
creates tags or replaces existing releases/assets.

Environment configuration:

| Environment | Allowed refs | Required reviewer |
| --- | --- | --- |
| `camera-build` | `main` branch | None |
| `camera-release` | `v*` tags only | None |
| `release-draft` | `v*` tags only | None |
| `release-publish` | `v*` tags only | `goliatone` (self-review allowed) |

Tag pushes now authorize use of the signing credentials. Limit who can push
release tags accordingly. Keep `CAMERA_SIGN_IDENTITY`, `CAMERA_TEAM_ID` and
`CAMERA_NOTARY_PROFILE` as `camera-release` environment variables. The hosted
runner imports the certificate into a private temporary Keychain and creates the
notarization profile there; it does not reuse the local Mac's Keychain. An
always-run cleanup removes the temporary Keychain and certificate. Signing
secrets are scoped to the credential setup step.

Manual dispatch remains available for development builds from `main`, with
`distribution=false` and `create_draft=false`. A manual signed dispatch requires
an existing matching version tag and can create a draft, but never selects the
publication job. Neither build starts a camera. Commit/push these workflow changes
before creating the first tag that should use this flow; existing tags/runs are
not upgraded retroactively.

#### Provision hosted signing secrets once

1. Open **Keychain Access → login → My Certificates**. Expand
   **Developer ID Application: Emiliano Burgos (ZY627Q5M24)** and confirm its private
   key is underneath. Select the identity, choose **File → Export Items**, and
   save as **Personal Information Exchange (.p12)** outside the repository.
   Set an export password and handle any macOS authorization prompt yourself.
   A `.cer` file alone does not include the required private key.
2. Have an Apple app-specific password available for the notarization account.
   The local `camstream-notary` profile cannot be copied to GitHub by name alone;
   the hosted runner needs its own credentials. Never put them in chat or Git.
3. Run this locally in Terminal, substituting the exported file path:

   ```sh
   python3 macos/provision-release-secrets.py --certificate "$HOME/Desktop/camstream-signing.p12"
   ```

   The helper checks signing tag restrictions and the publication review gate, securely prompts for the export
   password and Apple app-specific password, and sends values through stdin to
   `gh secret set`. It creates `CAMERA_CERTIFICATE_P12_BASE64`,
   `CAMERA_CERTIFICATE_PASSWORD`, `CAMERA_APPLE_ID`, and `CAMERA_APP_PASSWORD` in
   `camera-release`. Secret values never appear in arguments or output. Keep the
   exported `.p12` private and outside the checkout; the helper does not delete it.
4. Verify names only:

   ```sh
   gh secret list --repo goliatone/go-cam-stream --env camera-release
   ```

For a manual signed draft of an existing version tag (substitute its version):

```sh
gh workflow run release-artifacts.yml --repo goliatone/go-cam-stream \
  --ref vMAJOR.MINOR.PATCH -f version=MAJOR.MINOR.PATCH -F distribution=true -F create_draft=true
```

Normal new releases need only the tag push, followed by **release-publish** review
in GitHub Actions. Do not also manually dispatch the same version: draft creation
refuses an existing release instead of overwriting assets. Both build jobs must
pass before a draft is created; a failed build never reaches publication review.
Download drafts while signed into GitHub.

Migration: `v0.1.2` remains the historical failed candidate and `v0.1.3` retains its
original draft-only workflow. This setup change does not approve or publish either
release. Existing release validation and minimum-OS requirements still apply.

References: [GitHub macOS certificate setup](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications),
[environment protection](https://docs.github.com/en/actions/how-tos/deploy/configure-and-manage-deployments/manage-environments).

### Install, update and rollback

For a signed release, download its camera DMG, open it and drag **Camstream Camera**
to **Applications**. Eject the disk and open the app from Applications. Allow camera
access, save settings, then explicitly Start Camera. Offline copying of that same
stapled app/DMG needs no development tools or dependency downloads.

Quit before updating. Replace the complete app with the new version, then reopen;
do not merge bundle contents or run two copies. Roll back by quitting and replacing
it with the prior supported version. Both preserve settings/credentials in
`~/Library/Application Support/camstream-camera-app`. Removing the app also leaves
those settings intact. There is no automatic update, login agent or automatic
capture. Do not change Gatekeeper policy to validate a development artifact.

## Permission and lifecycle proof

This is the small permission/lifetime harness retained for isolated permission and helper-lifetime checks. It is a local development artifact, not an installable release.
It consumes the prepared runtime pack pinned in `runtime.lock.json`.

On the tested Apple Silicon/macOS 26 build host:

```sh
python3 macos/build-proof.py
# Or supply the exact locked archive from offline storage:
python3 macos/build-proof.py --runtime-pack /path/to/camera-runtime-1-darwin-arm64-macos26.0.tar.gz
```

Open `dist/Camstream Camera Proof.app` in **Finder**. Request Permission, then
Start. It selects the built-in webcam, publishes video only on
`rtsp://localhost:18554/macbook`, and shows actual producer frame progress.
Launch always starts stopped. Stop cancels a pending Start as well as active capture; closing the window preserves
the app; Quit waits for helper cleanup. Denial requires changing this app's
Camera permission in System Settings and relaunching. Never substitute a
Terminal-launched FFmpeg capture for this permission check.

After settings have been saved once, **Probe Denial** is available only when
AVFoundation reports denied access. This test-only action attempts the bundled
capture path directly: expect `capture_stopped` / `capture_failed`, zero frames,
and cleanup of the temporary RTSP listener. It checks OS enforcement beyond the
normal Start button's permission guard. Re-enable permission and relaunch to
verify recovery. An ad-hoc rebuild can request permission again despite keeping
the bundle identifier. The production app preserves its camera grant across the
locally validated Developer ID 0.1.0 → 0.1.1 update and rollback on macOS 26.7.1.

The proof identity is `com.goliatone.camstream.camera.proof`. Its private settings
and sanitized `proof.jsonl` are under
`~/Library/Application Support/camstream-camera-native-proof`. Credentials and
connection exports are mode 0600; they are not packaged in the app or logged.
The production CLI's settings and port are separate.

The builder verifies the archive against the repository lock, checks its complete
file inventory/target, copies runtime, sources and notices, builds Swift/Go, and
signs the nested bundle. FFmpeg statically includes its non-system dependencies;
only Apple system libraries/frameworks remain dynamically linked.
It refuses an existing output. Use `--output PATH.app` for another build.
All app runtime lookup is bundle-relative with no PATH fallback or downloads.
Ordinary app builds are offline by default and never rebuild dependencies. The
cache is `.cache/camera-runtime/packs`; `--cache DIR` or `--runtime-pack FILE`
supports preseeded storage. `--download` permits fetching only a URL recorded in
the reviewed lock. The current lock controls the exact pack and any approved download URL.
Runtime-1 and runtime-2 are publicly hosted as separately pinned prereleases.
The current runtime-3 lock selects a locally verified audio-capable pack with no
hosted URL. Supply that matching pack or preseed the cache; runtime-1/runtime-2
cannot satisfy the new lock. No runtime-3 publication is implied.

**Signing boundary:** proof/development builds use ad-hoc hardened signatures,
including FFmpeg. Static non-system dependencies remove the T04 dylib Team ID
problem. The separate release packager signs nested code with Developer ID and
notarizes/staples both app and DMG. Local signed permission, real capture,
Stop/Quit/crash cleanup and update/rollback checks pass on macOS 26.7.1. No system
security setting or Gatekeeper policy is changed. Hosted download and minimum
macOS 26.0 validation remain pending; Intel is not certified.

## Building a dependency pack

Prerequisites: Apple Silicon Mac, Xcode tools/SDK and Python 3.10+.
Go is needed for app/CLI builds, not the dependency build. No Homebrew binaries,
libraries, pkg-config, autoconf or automake are selected from the host. The build
bootstraps the pinned pkgconf source and uses a system-only PATH and private prefix.

```sh
# Download exact locked inputs if missing, then build from a fresh work directory.
python3 macos/build-runtime.py --work /private/tmp/camera-runtime-build
# Offline build: preseed every filename in runtime.lock.json sources first.
python3 macos/build-runtime.py --offline --cache /path/to/cache --work /private/tmp/camera-runtime-offline
# Promote the exact verified T01 candidate without recompiling its tools:
python3 macos/build-runtime.py --offline \
  --candidate /private/tmp/camstream-audio-t01-6/candidate \
  --work /private/tmp/camera-runtime-3-promotion
```

The source cache is `CACHE/sources`. A wrong checksum is an error, never silently
replaced. The builder compiles FFmpeg 9.0.2 and pinned x264 with a fixed reduced
feature set, pinned static libopus 1.6.1 and static libraries, and copies the exact
official MediaMTX 1.21.3 arm64 release. Runtime-3 retains T01’s codec/filter options
and both ordered FFmpeg patches: exact microphone UID/discovery and bounded audio
FIFO. Their SHA-256 values are recipe inputs; the builder verifies each patch and
applies it to pristine source with zero fuzz. Its capability gate requires
AVFoundation, libx264, H.264/Opus decode, libopus encode, both UID options, ffprobe,
RTSP/TCP and preview/null-sink support. Lavfi/testsrc2 and concat are retained for
camera-free validation and private-input reader probes. Upstream-selected RTSP
dependencies are recorded in BUILDINFO; this is not a general FFmpeg distribution.

`--candidate` requires the lock-pinned proof BUILDINFO digest, identical target,
source pins, configure options and ordered patches, plus every candidate tool hash.
Promotion preserves the verified tool code and applies pack signatures; the original
candidate provenance is included. The normal source-build path remains available.
Neither mode activates sensors: built-in smoke checks use lavfi synthetic video.

The resulting archive and candidate `.record.json` go into `CACHE/packs` by
default. Review the candidate hash into `runtime.lock.json` → `packs.darwin-arm64`
after validating it. The builder does not change the lock, publish an asset or
guess a release URL. A dependency/recipe change requires a rebuilt pack and new
approved archive hash; a cached pack with a different recipe/target is rejected.
Choose a new runtime ID for a distributed dependency revision. App versions and
runtime IDs remain independent.

Pack layout: `tools/`, `licenses/`, `sources/`, `recipe/`, `BUILDINFO.json` and
`MANIFEST.json`. It includes the exact FFmpeg/x264/Opus/pkgconf source archives, both patches,
modified-source notices, build scripts, configuration, provenance, dependency
inspection and upstream licenses. Tools include ffmpeg, ffprobe and MediaMTX.
FFmpeg/libx264 use GPL-2.0-or-later; Opus is BSD-3-Clause; MediaMTX is MIT; pkgconf
is an ISC build tool. Keep the accompanying sources/notices with distributed assets.
See [FFmpeg's upstream license guidance](https://ffmpeg.org/legal.html) and the
pack's license texts. Fixed inputs do not promise byte-identical output across
different SDKs/compiler versions; exact prepared archive hashes select artifacts.

Camera-free pack boundary checks:

```sh
python3 -m unittest discover -s macos -p 'test_*.py' -v
```

## Native/helper contract v1

Launch `Contents/Helpers/camstream-camera app-session --dir PRIVATE_DIRECTORY`
with inherited stdin/stdout pipes. Keep the stdin writer alive for the entire
app session; close unused pipe ends in the parent. Do not detach CLI `start`.
Use one reader to continuously drain stdout and correlate replies by `id`.
Stderr and process exit are startup/transport failures, not protocol messages.

Requests/replies are UTF-8 JSON objects separated by newlines. Each request has
`version: 1`, a unique caller-assigned `id` (1–64 alphanumeric, `_` or `-`, first
character alphanumeric) and `method`. Only `configure` accepts `config`.
Unknown fields/methods/versions, invalid config and extra JSON values are rejected.
Send one request at a time; there is no replay cache or retry deduplication.
Maximum request line is less than 16 KiB; queue capacity is eight. Oversized input,
queue overflow, EOF/read failure, signal or output failure ends the session.

```json
{"version":1,"id":"start-1","method":"start"}
{"version":1,"id":"start-1","result":{"state":"starting"}}
```

| Method | Result / behavior |
| --- | --- |
| `devices` | Array of `{index,name}` video devices; no screen/audio entries. Native shell obtains permission first. |
| `audio_devices` | Array of `{id,name}` microphones from metadata-only discovery; requires the patched runtime. |
| `config` | Non-secret `{device,listen,host,port,path,width,height,fps,audio_enabled,audio_device_id}`. Missing audio fields mean false/unset. |
| `configure` | Requires the complete `config` object with those fields; uses the existing configure service, preserves credentials and returns resolved settings. Refused during capture. |
| `start` | Acknowledges `starting`; poll `status` for actual readiness. Refuses an existing owned or external capture. |
| `status` | `{state,url?,frames?,audio:{enabled,state,packets}}`; URL never contains credentials. Audio-on `streaming` requires video frames and actual audio packets. |
| `stop` | Cancels and reaps capture owned by this session, then replies `stopped`. Does not stop another CLI owner's capture. |
| `export` | Recreates private connection files while stopped or live, preserving capture and credentials; returns `{configuration,environment}` file paths, never their contents. |
| `quit` | Same owned cleanup as stop, replies, then exits. Closing stdin may end the session before the queued reply; helper exit is the Quit completion boundary. |

On open, the helper emits `{"version":1,"event":"ready","result":{"protocol":1,"preview":1,"audio":1}}`.
Unexpected capture exit emits `capture_stopped` with `state: stopped` and an
optional classified error. No automatic restart. A second session using the same
state directory emits `session_error` / `busy` and exits without disturbing the
owner. The session lock lasts until owned cleanup finishes.

Errors are `{code,message}` with fixed messages: `invalid_request`, `busy`,
`not_configured`, `configuration_failed`, `device_unavailable`, `capture_failed`,
`status_unavailable`, `export_failed`. Malformed requests receive no echoed ID.
Raw FFmpeg/MediaMTX/config errors and reader secrets never cross this protocol.
Camera permission status remains native AVFoundation state; `capture_failed`
alone does not diagnose permission denial. The UI must combine these signals.
Status reads the supervisor snapshot; an immediate poll after `start` can still
be `stopped` before its control socket exists. Keep the acknowledged starting
state until the supervisor responds or `capture_stopped` arrives. There is no
unsolicited streaming event; poll status to observe progress.

EOF cancels in-flight operations and owned capture. The app-session process owns
the existing supervisor's lifetime pipe; killing either the native app or its
app-session helper closes that chain and the supervisor reaps the producer, audio observer, preview decoder and server.
This does not promise cleanup after SIGKILL of the supervisor itself or a frozen
OS. Closing the setup window must leave the app and its pipe alive.

Camera-free native ordering check (uses a temporary executable):

```sh
proof_check_dir=$(mktemp -d)
xcrun swiftc macos/CameraProof/StartIntent.swift macos/CameraProofTests/main.swift -o "$proof_check_dir/start-intent-tests"
"$proof_check_dir/start-intent-tests"
rm -rf "$proof_check_dir"
```

Focused Go checks (leave `CAMSTREAM_CAMERA_BUNDLE` unset for camera-free tests):

```sh
go test -race ./internal/maccamera ./internal/cameracli ./cmd/camstream-camera
go vet ./internal/maccamera ./internal/cameracli ./cmd/camstream-camera
golangci-lint run ./internal/maccamera/... ./internal/cameracli/... ./cmd/camstream-camera/...
```

Native hardware evidence is recorded in the macos-camera ctx spec. The proof
has no preview or recording and is independent of the mixed Kasa playback gate.

## Optional microphone backend (audio feature v1)

The T03 backend and T05 native controls implement audio feature v1. T04 development
assembly, real microphone transport/browser delivery, continuity and cleanup pass;
operator-confirmed audibility, physical sync and signed identity validation remain
pending. No app starts capture at
launch, Save, discovery or export. A saved opt-in requires a fresh explicit Start.

`audio_enabled` defaults to false. `audio_device_id` is the exact opaque macOS UID,
not a device label or index. Preserve case, spaces and colons; IDs must be valid
UTF-8, 1–1024 bytes, with no control characters. Audio-off may retain a valid saved
ID but never looks it up, requests audio capability or launches an audio observer.
Duplicate/prefix labels are valid; duplicate IDs fail discovery. Missing/replaced
IDs fail before capture; the patched runtime binds the exact ID again at input
creation, so enumeration-to-open removal cannot select a replacement.

CLI examples (the final command explicitly enables capture):

```sh
camstream-camera audio-devices
camstream-camera configure --device 'MacBook Pro Camera' --audio-enabled --audio-device-id 'EXACT_UID_FROM_DISCOVERY'
camstream-camera start
# In another terminal, or use Ctrl-C in the owner:
camstream-camera stop
```

Pass the existing video/network options when configuring: CLI flags start from
safe defaults, rather than merging omitted fields with saved settings. The same
Configure command/service backs the native helper and CLI. Audio-on needs the
complete verified runtime-3 tools, including ffprobe. It publishes H.264 and mono
48 kHz/64 kbit/s Opus with 20 ms packets from the same capture process. Audio-off
retains `video:none` and `-an`. There is no fallback, automatic restart or recording.

Discovery has a 20-second limit and a 128 KiB complete version-1 JSON payload
limit (also enforced after re-encoding for the helper envelope). The pinned enumeration exit code is 251; malformed, oversized, incomplete,
wrong-version or duplicate-ID inventories fail as a whole. Full IDs appear only
in private settings and explicit discovery/config replies, not failure messages.

The ready event adds `audio:1` without changing protocol 1: the existing Swift
transport reads ready fields additively. The native shell explicitly defaults absent config
fields in Codable and gates audio controls/requests on this capability. Older helpers
omit it and reject unknown audio config fields, even when false; use the legacy
request shape for audio-off or require a matching helper. Shared, synthetic-only
examples are in [fixtures/audio-protocol.json](fixtures/audio-protocol.json).
T05 keeps microphone identity byte-exact in selection keys and config equality,
including Unicode strings that Swift otherwise considers canonically equal.

`audio.state` is `disabled`, `waiting`, `live`, `stopping`, `stopped` (enabled but
inactive), or `failed`. Packets count actual increasing-timestamp Opus packets,
not amplitude: silence is live audio. Video frames alone cannot produce audio-on
`streaming`. The first audio deadline is 20 seconds from Start (passed into the
worker); each subsequent audio packet resets a five-second gap timer. The
supervisor owns a bounded, authenticated loopback ffprobe reader. Its input file is
0600 under the private runtime directory; no password is in argv or diagnostics.
Malformed observer output, observer exit, missing audio, a gap or producer failure
ends the whole session. Each child gets at most three seconds before kill/reap;
Stop confirms cleanup within the existing 12-second limit. Packet metadata is
bounded in memory; no audio samples or images are saved. Native preview stays
silent and independent of the audio watchdog.

Audio errors are fixed `{code,message}` values: `audio_selection_invalid`,
`audio_device_unavailable`, `audio_discovery_failed`, `audio_runtime_unsupported`,
`audio_startup_timeout`, `audio_stalled`, `audio_observer_failed` and
`audio_capture_failed`. The worker sends only an allowlisted code over a private
result pipe; raw runtime errors are discarded. `capture_stopped` includes the
failure and stopped/failed audio state. Within that app session, subsequent status
replies retain the failure until explicit Stop, successful Configure or a new
Start. Stop waits for and discards the old run's result before acknowledging, so a
late failure cannot overwrite a newer session. A fresh helper starts stopped.
Permission-required/denied labels come from native AVFoundation state; a
backend capture error alone cannot establish that permission was denied.

The exported server profile has `audio: true` only for an enabled configuration.
Reimport/reload that profile through the existing server configuration path, then
explicitly unmute the browser. Export does not update a running server, and browser
mute changes playback only. Development delivery passes; operator-confirmed
audibility, physical sync and signed identity validation remain T04.

To update an existing server, stop capture only to change and save microphone
settings. Export connection files before or after restarting capture. Use a server build with the `rtsp` provider; older
EC70-only builds reject this camera. Merge the exported camera and its grant into
the existing YAML, replacing `REPLACE_WITH_YOUR_SERVE_LOGIN` with the existing
authorized operator. Supply the exported `MAC_CAMERA_USERNAME` and
`MAC_CAMERA_PASSWORD` through the service's private environment file. Preserve
other cameras, grants and secrets. Ensure `limits.max_sources` covers the resulting
catalog and the configured backend port pools still cover those limits.

Run `camstream config validate --config /path/to/config.yaml` before the controlled
server restart. Validation checks configuration without connecting to the camera;
it does not prove credential correctness or delivery. Start the Mac app explicitly,
select its camera in the browser and choose **Unmute**. A previously imported
`audio: false` profile remains video-only until it is updated and the server reloads.

### Downgrading to a video-only helper

Old helpers strictly reject the new JSON keys. Stop capture, quit the app, then
use the new helper's `export-legacy --dir PRIVATE_DIRECTORY`. It writes a separate
0600 `camera-legacy.json` with the original eight video/network fields; it preserves
current settings and credentials and refuses an active supervisor lock. While
stopped, back up `camera.json` privately and replace it with that generated file
before installing/launching the old bundle. Keep `credentials.json` unchanged.
Use the old app's Export action to regenerate a video-only server profile and
reload it; do not reuse an earlier `audio: true` export. Restore the private backup
only after returning to the matching audio-capable helper. No migration occurs
silently on launch.

Synthetic service checks with a verified pack (no sensors):

```sh
CAMSTREAM_AUDIO_RUNTIME='/absolute/path/to/runtime/tools' go test ./internal/maccamera -run '^TestAudioActualRuntime$' -v
```

## Local preview protocol v1

The native setup preview attaches only after Start Camera has produced streaming
frames. `ready.result.preview: 1` advertises support; old CLI/proof operations keep
protocol version 1. Control remains NDJSON. No frame bytes or credentials enter it.

- `preview_attach` (no config): only the owning streaming app session may attach.
  Result: `{version:1,socket:"/tmp/camstream-camera-UID-HASH/preview-GENERATION.sock",generation:"16-hex-digits"}`.
  Only one subscription is admitted. Startup, duplicate attach and unavailable
  transport return fixed `preview_unavailable`; capture remains unchanged.
- `preview_detach` requires `preview_generation` matching the attach result.
  Detach is idempotent; an old generation cannot stop a newer subscription. A
  stopped session needs no cleanup. Existing methods reject this extra field.

The socket is mode 0600 in the existing owner-only short runtime directory (the
Application Support path can exceed Darwin's Unix socket limit). It accepts one
connection within five seconds and removes its socket name after accept. The app
sends no bytes; EOF or any client input ends the subscription. The supervisor owns
the decoder, private input file and cancellation. Private concat input carries
RTSP credentials; arguments carry only its path. No additional TCP listener or
anonymous reader permission is introduced.

Each frame is a 48-byte header followed by packed BGRA8 pixels (opaque alpha,
8 bits per channel, top row first). All header integers are unsigned big-endian:

| Offset | Bytes | Meaning |
| --- | --- | --- |
| 0 | 4 | ASCII `CSPR` |
| 4 | 2 | Version 1 |
| 6 | 2 | Header length 48 |
| 8 | 8 | Generation, equal to the attach result interpreted as hex |
| 16 | 8 | Increasing sequence, starting at 1 |
| 24 | 8 | Decoder delivery timestamp, Unix milliseconds |
| 32 | 4 | Width, 1–640 |
| 36 | 4 | Height, 1–360 |
| 40 | 4 | Stride, exactly width × 4 |
| 44 | 4 | Payload bytes, exactly stride × height, at most 921600 |

Frames preserve the configured aspect ratio within 640×360 and run at up to
15 fps. Consumers must validate the header before allocating the payload, read
complete frames across arbitrary socket chunks and reject stale generations,
non-increasing sequences and malformed/truncated frames. The shared fixture is
`internal/maccamera/testdata/preview-frame-v1.json` (synthetic pixels only).
The producer retains one newest pending frame. A two-second blocked socket write,
eight-second first-frame timeout or five-second decoder stall ends preview.
Normal capture Stop, Quit and app/helper owner loss reap preview too. Decoder
failure alone never stops the camera; retry explicitly attaches a new generation.

Camera-free actual-runtime checks use a verified tools directory:

```sh
CAMSTREAM_PREVIEW_RUNTIME='/path/to/verified/runtime/tools' python3 -m unittest discover -s macos -p test_preview_runtime.py -v
CAMSTREAM_PREVIEW_RUNTIME='/path/to/verified/runtime/tools' go test -race ./internal/maccamera -run TestPreview
```

These checks publish synthetic video on a temporary loopback RTSP listener and
retain no images. They do not activate a webcam or certify the native UI.

### Using the live preview

After saving settings and choosing **Start Camera**, Setup & Settings displays
an aspect-fit, unmirrored view of the published stream. No preview capture occurs
before Start. In private-network mode, Start also makes the authenticated stream
available to configured readers; this is not a preview-only mode.

Closing, minimizing, fully covering the window, or switching to Connection Details
pauses preview processing while streaming continues. Reopening the visible setup
panel reconnects. Stop clears the preview; Quit stops all capture. If preview
fails, **Retry Preview** reconnects without restarting an otherwise healthy stream.
A stale image is cleared after five seconds. Local preview does not establish
that a remote Camstream server can reach this Mac.

Runtime-2 adds bounded raw BGRA output for preview (up to 640×360 at 15 fps).
Runtime-3 preserves those preview components and adds the verified Opus/UID/FIFO
runtime. The pack alone does not add microphone configuration, supervision or UI.
T03/T05 now supply those source changes and production permission metadata. The
older runtime-check development assembly remains camera-only. The T04 local app
includes microphone permissions and passes real transport/browser delivery;
operator-confirmed audibility, physical sync and signed identity validation remain
pending. Earlier
video-only artifacts do not establish optional-audio delivery.

For video-only browser onboarding, keep capture running and export or reveal both
connection files in Connection Details. In the native development viewer, choose
Add Camera and import those files. Review the endpoint, test preview, save, then
choose Watch Camera. YAML alone requires entering credentials. Exported access
grants are ignored; the saved camera belongs to the signed-in creator.

`localhost` means the server's machine. Same-Mac development uses IPv4 loopback;
a Linux/container/remote host needs a reachable companion address and its Private
network setting. Imported hosts are never silently rewritten. The selected numeric
address stays fixed after testing and saving. Only trusted operators/upstreams
should be configured: go2rtc redirects are trusted, not contained by admission.
