import AppKit

// Host invitation copying from the call page and the menus, and the native
// state of a browser-hosted room. Fixture helpers only; copies are recorded
// instead of reaching the pasteboard.
func runInvitationTests(conversations: [String: Any], check: (Bool, String) -> Void) {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    let waiting = conversations["waiting"] as! [String: Any]
    let room = waiting["room_id"] as! String
    let page = "https://call.example.test/r/" + room
    let link = page + "#invitation-secret"
    func prejoin(_ role: String) -> [String: Any] {
        var result: [String: Any] = ["page_url": page, "admission": ["room": waiting, "role": role, "token": "fixture-grant"]]
        if role == "host" { result["invitation_url"] = link }
        return ["result": result]
    }
    var copied: [String] = []
    let helper = FixtureSharingCamera()
    let model = CameraModel(client: helper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
    model.writeClipboard = { copied.append($0) }
    model.launch()
    let menus = AppMenus(model: model)
    func callItem(_ title: String) -> NSMenuItem? {
        menus.main.items.first { $0.title == "Call" }?.submenu?.items.first { $0.title == title }
    }
    func statusItem(_ title: String) -> NSMenuItem? { menus.status.items.first { $0.title == title } }

    check(callItem("Copy Invitation").map(menus.validateMenuItem) == false && statusItem("Copy Invitation")?.isHidden == true,
          "Copy Invitation is unavailable without a hosted call")

    helper.conversationReply = prejoin("host")
    model.startConversation()
    guard let surface = model.conversationSurface else { check(false, "a host call opens"); return }
    check(model.copyConversationInvitation(from: surface) && copied == [link] && model.conversationNotice == nil,
          "the host page copies its invitation through the app and adds no second note")
    menus.update()
    check(callItem("Copy Invitation").map(menus.validateMenuItem) == true && statusItem("Copy Invitation")?.isHidden == false,
          "the Call and status menus offer Copy Invitation while hosting")
    model.copyConversationInvitation()
    check(copied == [link, link] && model.conversationNotice?.text == "Invitation copied." && model.conversationNotice?.problem == false,
          "the menu copy confirms in the window")
    model.closeConversation()
    check(!model.copyConversationInvitation(from: surface) && copied.count == 2,
          "a closed call page cannot copy the invitation")

    helper.conversationReply = prejoin("guest")
    model.openConversationInvitation("camstream://conversation/join?v=1#fixture")
    if let guestSurface = model.conversationSurface {
        check(!model.copyConversationInvitation(from: guestSurface) && !model.canCopyConversation && copied.count == 2,
              "a guest page cannot copy an invitation")
    } else { check(false, "a guest call opens") }
    menus.update()
    check(statusItem("Copy Invitation")?.isHidden == true, "guests do not see Copy Invitation")
    model.closeConversation()

    check(ConversationView.browserStatus(.waiting) == "Waiting for your guest" &&
          ConversationView.browserStatus(.connected) == "Your guest is connected" &&
          ConversationView.browserStatus(.reconnecting) == "Reconnecting to your guest" &&
          ConversationView.browserStatus(nil) == "Hosting in your browser",
          "a browser-hosted room shows the owner's room state")
    _ = model.quit()

    let reconnectingHelper = FixtureSharingCamera()
    let reconnecting = CameraModel(client: reconnectingHelper, permissions: FixturePermissions(), directory: directory)
    reconnecting.launch()
    defer { _ = reconnecting.quit() }
    reconnectingHelper.heldConversationMethods = [.openLink]
    reconnecting.openConversationInvitation("camstream://conversation/join?v=1#already-sent")
    reconnectingHelper.onFailure?(.disconnected)
    reconnectingHelper.onExit?()
    let newInvitation = "camstream://conversation/join?v=1#during-outage"
    reconnecting.openConversationInvitation(newInvitation)
    var sleepAcknowledged = false
    reconnecting.prepareForSleep { sleepAcknowledged = true }
    reconnecting.wakeFromSleep()
    check(sleepAcknowledged && reconnectingHelper.incomingInvitations.count == 1,
          "sleep during an outage does not submit an invitation to the lost helper")
    reconnectingHelper.launchFailure = .busy
    reconnecting.retryHelper()
    check(reconnectingHelper.incomingInvitations.count == 1 && !reconnecting.ready,
          "the pending invitation stays queued across a failed retry")
    reconnectingHelper.launchFailure = nil
    reconnectingHelper.heldConversationMethods = []
    reconnectingHelper.conversationReply = prejoin("guest")
    reconnecting.retryHelper()
    reconnecting.openConversationInvitation(newInvitation)
    check(reconnectingHelper.incomingInvitations == ["camstream://conversation/join?v=1#already-sent", newInvitation] &&
          reconnecting.conversationPrejoin?.admission.role == .guest && !reconnecting.conversationCapture,
          "the new invitation survives sleep and failed reconnect and is submitted once with capture off")

    let sleepingHelper = FixtureSharingCamera()
    sleepingHelper.conversationReply = prejoin("guest")
    let sleeping = CameraModel(client: sleepingHelper, permissions: FixturePermissions(), directory: directory)
    sleeping.launch()
    defer { _ = sleeping.quit() }
    sleepingHelper.sharing.replies[.hostSleep] = ["result": [:]]
    sleeping.prepareForSleep { }
    sleeping.openConversationInvitation(newInvitation)
    check(sleepingHelper.incomingInvitations.isEmpty, "sleep acknowledgement does not allow admission before wake")
    sleeping.wakeFromSleep()
    check(sleepingHelper.incomingInvitations == [newInvitation], "wake submits the queued invitation once when the helper is ready")

    let wakingHelper = FixtureSharingCamera()
    wakingHelper.conversationReply = prejoin("host")
    wakingHelper.heldConversationMethods = [.end]
    wakingHelper.sharing.hold = [.hostSleep]
    let waking = CameraModel(client: wakingHelper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
    waking.launch()
    defer { _ = waking.quit() }
    waking.startConversationInBrowser()
    check(waking.hostInBrowser, "a hosted call is open for the wake-during-cleanup fixture")
    let sleepPause = "Sharing pauses while the Mac is going to sleep."
    var acknowledgements = 0
    waking.prepareForSleep { acknowledgements += 1 }
    check(waking.sharing.shareBlockedReason == sleepPause && wakingHelper.pendingConversation[.end] != nil,
          "sleep pauses Sharing while the previous call is still closing")
    waking.wakeFromSleep()
    waking.openConversationInvitation(newInvitation)
    check(wakingHelper.incomingInvitations.isEmpty && acknowledgements == 0,
          "an invitation opened during wake waits for old call cleanup")
    wakingHelper.conversationReply = prejoin("guest")
    wakingHelper.pendingConversation.removeValue(forKey: .end)?(["result": [:]])
    let revokeDeadline = Date().addingTimeInterval(1)
    while wakingHelper.sharing.pending.isEmpty && Date() < revokeDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(wakingHelper.sharing.methods.filter { $0 == .hostSleep }.count == 1 && acknowledgements == 0 &&
          wakingHelper.incomingInvitations.isEmpty, "late cleanup still revokes Sharing before allowing a new call")
    wakingHelper.sharing.deliver(["result": [:]])
    check(acknowledgements == 1 && waking.sharing.shareBlockedReason != sleepPause && waking.conversationAvailable,
          "late sleep cleanup leaves Sharing available after wake")
    check(wakingHelper.incomingInvitations == [newInvitation] && waking.conversationPrejoin?.admission.role == .guest,
          "the invitation opened during wake is redeemed after both cleanups without reopening it")
    waking.openConversationInvitation(newInvitation)
    check(wakingHelper.incomingInvitations == [newInvitation], "reopening the admitted link does not redeem it twice")

    let repeatedHelper = FixtureSharingCamera()
    repeatedHelper.sharing.hold = [.hostSleep]
    let repeated = CameraModel(client: repeatedHelper, permissions: FixturePermissions(), directory: directory)
    repeated.launch()
    defer { _ = repeated.quit() }
    var oldSleepAcknowledged = false, newSleepAcknowledged = false
    repeated.prepareForSleep { oldSleepAcknowledged = true }
    repeated.wakeFromSleep()
    repeated.prepareForSleep { newSleepAcknowledged = true }
    check(oldSleepAcknowledged && !newSleepAcknowledged && repeatedHelper.sharing.pending.count == 2,
          "a new sleep transition replaces the previous acknowledgement")
    repeatedHelper.sharing.deliver(["error": ["code": "transport"]])
    check(repeated.ready && !newSleepAcknowledged && repeated.sharing.shareBlockedReason == sleepPause,
          "a stale revoke reply cannot shut down the helper or acknowledge the new sleep")
    repeatedHelper.sharing.deliver(["result": [:]])
    repeated.wakeFromSleep()
    check(newSleepAcknowledged && repeated.conversationAvailable && repeated.sharing.shareBlockedReason != sleepPause,
          "the current sleep reply and wake restore Sharing and calls")
}
