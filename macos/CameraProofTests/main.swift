// Camera-free ordering regressions for the state used by the native proof.
func expect(_ value: Bool, _ message: String) {
    precondition(value, message)
}

var intent = StartIntent()
expect(!intent.consume(nil, succeeded: true), "unsolicited event started capture")
intent.begin("configure-1")
intent.cancel() // Stop while configure/device discovery is pending.
expect(!intent.consume("configure-1", succeeded: true), "late configure restarted after Stop")

intent.begin("configure-2")
intent.begin("configure-3") // Only the latest explicit Start may finish.
expect(!intent.consume("configure-2", succeeded: true), "superseded Start was honored")
expect(!intent.consume("status-4", succeeded: true), "poll consumed Start intent")
expect(intent.consume("configure-3", succeeded: true), "current Start was lost")
expect(!intent.consume("configure-3", succeeded: true), "duplicate reply restarted capture")

intent.begin("configure-5")
expect(!intent.consume("configure-5", succeeded: false), "failed configuration started capture")
expect(!intent.consume("configure-5", succeeded: true), "failed intent remained active")
intent.begin("configure-6")
intent.cancel() // Quit or helper/transport failure.
expect(!intent.consume("configure-6", succeeded: true), "terminated intent remained active")
intent.begin(nil) // Sending configure failed.
expect(!intent.consume(nil, succeeded: true), "failed send created Start intent")
print("PASS: 10 native Start intent ordering checks (no camera access)")
