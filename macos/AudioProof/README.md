# T01 microphone proof

Development harness for `macos-microphone-audio/T01`. This is not the production
app or runtime pack. Audio starts only through **Start Audio**; launch and
permission actions leave capture stopped. The unchanged helper's status is video
status only. `observe.py` independently checks actual authenticated audio packets.

Build from this repository with the hash-pinned source archives preseeded in
`.cache/camera-runtime/sources` (see `candidate.lock.json`):

```sh
python3 macos/AudioProof/build_candidate.py --work /private/tmp/audio-proof-build
python3 macos/AudioProof/build_app.py \
  --candidate /private/tmp/audio-proof-build/candidate \
  --output 'dist/Camstream Audio Proof.app'
```

Both destinations must be new. The recipe uses the repository's existing
`macos/runtime_pack.py`; the copied recipe is not a standalone installer.
The candidate includes source archives, license notices, patch, exact input lock,
commands and binary hashes. The app uses ad-hoc hardened signatures, its own
`com.goliatone.camstream.camera.audio-proof` identity, state under
`~/Library/Application Support/camstream-audio-native-proof`, and loopback port
18557. It does not modify the installed Camstream Camera settings.

The proof-only Go executable named `ffmpeg` inserts exact-UID and Opus arguments,
then replaces itself with `ffmpeg-real`. This preserves the existing supervisor
PID/lifetime chain without adding production audio configuration. The selector
patch rejects ambiguous, missing and disconnected identities and audio-open
errors. A second patch replaces the upstream single unread audio slot with a
512-entry FIFO capped at 4 MiB of queued sample payload; overflow fails capture.
Native selection initially chooses the single MacBook-labelled device
and passes its UID; labels are not used by the patched capture selector.

Sensor-free checks:

```sh
go test -race ./macos/AudioProof/proxy
xcrun clang -fno-objc-arc -framework Foundation \
  -I /private/tmp/audio-proof-build/source/ffmpeg/ffmpeg-9.0.2/libavdevice \
  macos/AudioProof/selector_test.m -o /private/tmp/audio-selector-test
/private/tmp/audio-selector-test
xcrun clang -fno-objc-arc -framework Foundation \
  -I /private/tmp/audio-proof-build/source/ffmpeg/ffmpeg-9.0.2/libavdevice \
  macos/AudioProof/audio_queue_test.m -o /private/tmp/audio-queue-test
/private/tmp/audio-queue-test
CAMSTREAM_AUDIO_PROOF_RUNTIME=/private/tmp/audio-proof-build/candidate/tools \
  python3 -m unittest macos/AudioProof/test_candidate.py
```

For authorized hardware verification, open the app in Finder. Grant camera
permission and deny microphone permission first; check video-only Start,
audio-on guard and **Probe Denial**. Recover microphone access through System
Settings and relaunch; recovery must remain stopped. Explicitly start audio,
then run the reader with `--tools` pointing at the app's `Contents/Helpers`:

```sh
python3 macos/AudioProof/observe.py \
  --tools 'dist/Camstream Audio Proof.app/Contents/Helpers' --seconds 15
```

The reader stores credentials only in a temporary mode-0600 concat input, reaps
its child and removes the file on exit. It prints packet counts and timestamps,
never samples. The measured T01 recommendation is a 20-second first-audio bound from explicit Start
and a 5-second subsequent stall bound. This standalone reader measures attachment
time; T03 must integrate the start deadline and whole-chain shutdown. Arrival/packet
timestamps alone do not establish physical lip sync. Repeat the synchronized visible/acoustic startup and ten-minute event checks
whenever audio capture, selection, codec or timestamp behavior changes.

Verify permission, silence/audible-input, missing-UID and
Stop/Quit/app/helper-loss checks against the same bytes. Use **Stop**, then
**Quit**, and verify the proof listener and children are gone. Do not run an
unattended capture or promote the candidate while T01 evidence is incomplete.
Current results and remaining work are canonical in
`.ctx/specs/macos-microphone-audio/support/evidence/t01-capture-permission.md`.

The proof adapter keeps publishing diagnostics in a private
`capture-diagnostic.log` because the unchanged helper discards producer stderr.
Enumeration keeps its original stderr contract. Do not copy raw diagnostics into
evidence; retain only sanitized failure classifications.

For physical timing, `sync_probe.py --tools <app>/Contents/Helpers --seconds 20`
decodes a bounded in-memory window and prints a local comparison URL. After its READY cue, make one clap at face height with both hands fully visible. The temporary page shows frames around the
strongest acoustic impulse; select the first frame with hand contact. It reports
audio onset minus that frame's PTS, with approximately 33.3 ms video resolution at
30 fps. Check the prior frame to bracket contact. Repeat after ten minutes of the
same publishing session. The page serves no audio payload and sends no media to
external services. Close the page and terminate its server after each comparison;
only numeric timing results belong in evidence. Packet continuity counters in
`observe.py` detect missing sample durations separately from reader-arrival gaps.
