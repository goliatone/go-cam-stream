import Foundation

@MainActor
private final class HeldServerProbes {
    private(set) var urls: [URL] = []
    private var replies: [Int: CheckedContinuation<ServerStatus, Never>] = [:]

    func check(_ url: URL) async -> ServerStatus {
        let index = urls.count
        urls.append(url)
        return await withCheckedContinuation { replies[index] = $0 }
    }

    func reply(_ index: Int, _ status: ServerStatus) { replies.removeValue(forKey: index)?.resume(returning: status) }
}

@MainActor
private func waitForServer(_ condition: () -> Bool) async -> Bool {
    for _ in 0..<150 {
        if condition() { return true }
        try? await Task.sleep(for: .milliseconds(10))
    }
    return condition()
}

@MainActor
func testServerMonitor() async {
    let local = ServerEndpoint("http://127.0.0.1:8087")
    check(local?.viewer.absoluteString == "http://127.0.0.1:8087/" && local?.health.path == "/healthz",
          "the local viewer URL and health probe use the configured development port")
    check(ServerEndpoint("http://[::1]:8087/")?.health.path == "/healthz", "IPv6 loopback uses the local probe")
    check(ServerEndpoint(" https://camstream.example.test/ ")?.health.path == "/api/health", "remote viewer uses the authenticated API probe")
    for invalid in ["http://example.test", "file:///tmp/camstream", "http://localhost:0", "http://localhost:65536",
                    "https://user:secret@example.test/", "https://example.test/viewer", "https://example.test/?q=value", "https://example.test/#link"] {
        check(ServerEndpoint(invalid) == nil, "invalid server origin is rejected")
    }

    let suite = "camstream-server-test-\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    let fixture = HeldServerProbes()
    let monitor = ServerMonitor(defaults: defaults, interval: .milliseconds(120), probe: fixture.check)
    check(monitor.viewerURL == nil && monitor.canCheck && monitor.address == ServerMonitor.defaultAddress,
          "the default address is editable and Server starts hidden")
    monitor.setVisible(true)
    let started = await waitForServer { fixture.urls.count == 1 }
    check(started && monitor.checking, "the visible window starts an asynchronous check")
    monitor.checkNow()
    await Task.yield()
    check(fixture.urls.count == 1, "Check Now coalesces with an in-flight check")

    monitor.address = "http://127.0.0.1:8087"
    monitor.checkNow()
    let changed = await waitForServer { fixture.urls.count == 2 }
    check(changed && fixture.urls.last == local?.health, "Check Now immediately probes the edited address")
    fixture.reply(0, .reachable)
    await Task.yield()
    check(monitor.viewerURL == nil && monitor.checking, "an obsolete response cannot reveal the Server tab or finish the new check")
    fixture.reply(1, .reachable)
    let reached = await waitForServer { monitor.viewerURL == local?.viewer }
    check(reached, "a successful current check supplies the URL for Open Viewer")
    let refreshing = await waitForServer { fixture.urls.count == 3 }
    check(refreshing && monitor.viewerURL == local?.viewer, "background checks keep the tab available while a refresh is pending")
    fixture.reply(2, .unavailable)
    let lost = await waitForServer { monitor.status == .unavailable }
    check(lost && monitor.viewerURL == nil && monitor.canCheck, "a failed refresh removes the tab and allows a retry")

    let nextPoll = await waitForServer { fixture.urls.count == 4 }
    check(nextPoll, "the app retries automatically while visible")
    monitor.setVisible(false)
    fixture.reply(3, .reachable)
    try? await Task.sleep(for: .milliseconds(250))
    check(fixture.urls.count == 4 && !monitor.checking && monitor.viewerURL == nil, "hiding the window cancels pending checks and future polls")
    check(ServerMonitor(defaults: defaults).address == local?.viewer.absoluteString, "the configured address survives a fresh monitor")
    monitor.address = ""
    monitor.setVisible(true)
    await Task.yield()
    check(fixture.urls.count == 4 && !monitor.canCheck && ServerMonitor(defaults: defaults).address.isEmpty,
          "clearing the address disables checks across launches")
}
