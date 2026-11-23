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

    check(CameraModel.browserStatus(.waiting) == "Waiting for your guest" &&
          CameraModel.browserStatus(.connected) == "Your guest is connected" &&
          CameraModel.browserStatus(.reconnecting) == "Reconnecting to your guest" &&
          CameraModel.browserStatus(nil) == "Hosting in your browser",
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

    let departingHelper = FixtureSharingCamera()
    departingHelper.conversationReply = prejoin("guest")
    let departing = CameraModel(client: departingHelper, permissions: FixturePermissions(), directory: directory)
    departing.launch()
    defer { _ = departing.quit() }
    departing.openConversationInvitation(newInvitation)
    check(departing.conversationPrejoin?.admission.role == .guest, "the previous guest seat is open")
    departingHelper.heldConversationMethods = [.leave]
    departing.closeConversation()
    departing.openConversationInvitation(newInvitation)
    let leaveFailed = ["error": ["code": "media_unreachable"]]
    departingHelper.pendingConversation.removeValue(forKey: .leave)?(leaveFailed)
    let closeDeadline = Date().addingTimeInterval(1)
    while !departing.canRetryConversationClose && Date() < closeDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(departingHelper.incomingInvitations == [newInvitation] && !departing.canStartConversation &&
          departing.conversationFeedback?.actions == [.retryConversationClose],
          "a failed Leave preserves the same-room invitation and offers cleanup retry")
    departing.perform(.retryConversationClose)
    check(departingHelper.pendingConversation[.leave] != nil && departingHelper.incomingInvitations == [newInvitation],
          "retry sends Leave again before redeeming the queued link")
    departingHelper.pendingConversation.removeValue(forKey: .leave)?(leaveFailed)
    let retryDeadline = Date().addingTimeInterval(1)
    while !departing.canRetryConversationClose && Date() < retryDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(departing.canRetryConversationClose && departingHelper.incomingInvitations == [newInvitation],
          "another failed cleanup keeps the invitation queued")
    departing.perform(.retryConversationClose)
    departingHelper.pendingConversation.removeValue(forKey: .leave)?(["result": [:]])
    let invitationDeadline = Date().addingTimeInterval(1)
    while departing.conversationPrejoin == nil && Date() < invitationDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(departingHelper.incomingInvitations == [newInvitation, newInvitation] && departing.conversationPrejoin?.admission.role == .guest,
          "successful cleanup redeems the queued link exactly once without reopening it")
    departingHelper.heldConversationMethods = []

    let endedHelper = FixtureSharingCamera()
    endedHelper.conversationReply = prejoin("host")
    let ended = CameraModel(client: endedHelper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
    ended.launch()
    defer { _ = ended.quit() }
    ended.startConversationInBrowser()
    check(ended.hostInBrowser && ended.conversationCapture, "the browser host holds its device lease")
    endedHelper.conversationReply = ["result": [:]]
    endedHelper.browserCapturePending = true
    ended.closeConversation()
    check(ended.conversationCapture && !ended.canStartConversation && ended.browserCaptureNeedsConfirmation,
          "End keeps the lease while the browser still owns capture")
    endedHelper.browserCapturePending = false
    let browserDeadline = Date().addingTimeInterval(4)
    while ended.conversationCapture && Date() < browserDeadline { RunLoop.current.run(until: Date().addingTimeInterval(0.02)) }
    check(!ended.conversationCapture && ended.canStartConversation, "browser stop acknowledgement releases the lease")

    endedHelper.conversationReply = prejoin("host")
    ended.startConversationInBrowser()
    endedHelper.onFailure?(.disconnected)
    endedHelper.onExit?()
    check(ended.conversationCapture && ended.browserCaptureNeedsConfirmation && !ended.quit(),
          "owner loss and Quit keep the lease until the browser tab is closed")
    var browserQuit = false
    ended.didFinishQuit = { browserQuit = true }
    ended.confirmBrowserCallClosed()
    check(!ended.conversationCapture && browserQuit, "confirming the closed browser tab releases capture and completes Quit")

    let sleepLeaveHelper = FixtureSharingCamera()
    sleepLeaveHelper.conversationReply = prejoin("guest")
    let sleepLeave = CameraModel(client: sleepLeaveHelper, permissions: FixturePermissions(), directory: directory)
    sleepLeave.launch()
    defer { _ = sleepLeave.quit() }
    sleepLeave.openConversationInvitation(newInvitation)
    sleepLeaveHelper.heldConversationMethods = [.leave]
    sleepLeave.closeConversation()
    sleepLeave.openConversationInvitation(newInvitation)
    sleepLeaveHelper.pendingConversation.removeValue(forKey: .leave)?(leaveFailed)
    let failedCloseDeadline = Date().addingTimeInterval(1)
    while !sleepLeave.canRetryConversationClose && Date() < failedCloseDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    sleepLeave.prepareForSleep {}
    sleepLeave.wakeFromSleep()
    sleepLeaveHelper.pendingConversation.removeValue(forKey: .leave)?(leaveFailed)
    let sleepExitDeadline = Date().addingTimeInterval(1)
    while !sleepLeave.canRetryHelper && Date() < sleepExitDeadline { RunLoop.current.run(until: Date().addingTimeInterval(0.01)) }
    check(sleepLeave.canRetryHelper, "failed Leave during sleep has reaped the original helper")
    sleepLeave.retryHelper()
    check(sleepLeaveHelper.guestRecoveryRequests == 1 && sleepLeaveHelper.incomingInvitations == [newInvitation],
          "replacement helper uses the original guest grant before submitting the queued invitation")
    sleepLeaveHelper.pendingConversation.removeValue(forKey: .leave)?(leaveFailed)
    let recoveryDeadline = Date().addingTimeInterval(1)
    while !sleepLeave.canRetryConversationClose && Date() < recoveryDeadline { RunLoop.current.run(until: Date().addingTimeInterval(0.01)) }
    check(sleepLeave.canRetryConversationClose && sleepLeaveHelper.incomingInvitations == [newInvitation],
          "a failed recovery still preserves the invitation after reconnect")
    sleepLeave.retryConversationClose()
    sleepLeaveHelper.heldConversationMethods = []
    sleepLeaveHelper.pendingConversation.removeValue(forKey: .leave)?(["result": [:]])
    let recoveredInviteDeadline = Date().addingTimeInterval(1)
    while sleepLeave.conversationPrejoin == nil && Date() < recoveredInviteDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(sleepLeaveHelper.incomingInvitations == [newInvitation, newInvitation] && sleepLeave.conversationPrejoin != nil,
          "recovered Leave opens the queued invitation once")

    let lostAdmissionHelper = FixtureSharingCamera()
    lostAdmissionHelper.guestAdmissionVersion = 1
    lostAdmissionHelper.heldConversationMethods = [.openLink, .leave]
    let lostAdmission = CameraModel(client: lostAdmissionHelper, permissions: FixturePermissions(), directory: directory)
    lostAdmission.launch()
    defer { _ = lostAdmission.quit() }
    lostAdmission.openConversationInvitation(newInvitation)
    let originalToken = lostAdmissionHelper.guestAdmissionTokens.first
    check(originalToken?.utf8.count == 43, "the app has a scoped guest token before receiving the admission")
    lostAdmission.closeConversation()
    lostAdmission.openConversationInvitation(newInvitation)
    lostAdmission.prepareForSleep {}
    lostAdmission.wakeFromSleep()
    lostAdmissionHelper.pendingConversation.removeValue(forKey: .openLink)
    lostAdmissionHelper.onFailure?(.sleepInterrupted)
    lostAdmissionHelper.onExit?()
    lostAdmission.retryHelper()
    let lostRecoveryDeadline = Date().addingTimeInterval(1)
    while lostAdmissionHelper.recoveredGuestToken == nil && Date() < lostRecoveryDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(lostAdmissionHelper.recoveredGuestToken == originalToken && lostAdmissionHelper.recoveredGuestInvitation == newInvitation &&
          lostAdmissionHelper.incomingInvitations == [newInvitation],
          "replacement helper releases the cancelled attempt before reopening the link, even without the first admission reply")
    lostAdmissionHelper.heldConversationMethods = []
    lostAdmissionHelper.conversationReply = prejoin("guest")
    lostAdmissionHelper.pendingConversation.removeValue(forKey: .leave)?(["result": [:]])
    let lostInviteDeadline = Date().addingTimeInterval(1)
    while lostAdmission.conversationPrejoin == nil && Date() < lostInviteDeadline {
        RunLoop.current.run(until: Date().addingTimeInterval(0.01))
    }
    check(lostAdmission.conversationPrejoin != nil && lostAdmissionHelper.incomingInvitations == [newInvitation, newInvitation] &&
          lostAdmissionHelper.guestAdmissionTokens.last != originalToken, "reopening uses a fresh grant after cleaning up the lost admission")

    let browserHelper = FixtureSharingCamera()
    browserHelper.conversationReply = prejoin("host")
    let browser = CameraModel(client: browserHelper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
    browser.launch()
    defer { _ = browser.quit() }
    browser.startConversationInBrowser()
    browserHelper.heldConversationMethods = [.status]
    browserHelper.conversationReply = ["result": [:]]
    browser.closeConversation()
    let staleStatus = browserHelper.pendingConversation.removeValue(forKey: .status)
    check(browser.browserCaptureNeedsConfirmation && staleStatus != nil, "manual release can overlap a pending browser status poll")
    browser.confirmBrowserCallClosed()
    browserHelper.conversationReply = prejoin("host")
    browser.startConversationInBrowser()
    browser.tick()
    let currentStatus = browserHelper.pendingConversation.removeValue(forKey: .status)
    var terminal = waiting
    terminal["state"] = "ended"
    staleStatus?(["result": terminal])
    check(browser.hostInBrowser && browser.conversationCapture && currentStatus != nil,
          "an old status reply cannot block the new lease's poll or release the new lease")
    browserHelper.heldConversationMethods = []
    currentStatus?(["result": terminal])
    check(!browser.conversationCapture, "the current browser's terminal status releases its capture lease")

    let quitHelper = FixtureSharingCamera()
    quitHelper.conversationReply = prejoin("host")
    let browserQuitModel = CameraModel(client: quitHelper, permissions: FixturePermissions(), directory: directory, openBrowser: { _ in true })
    browserQuitModel.launch()
    var quitObserved = false
    browserQuitModel.didFinishQuit = { quitObserved = true }
    browserQuitModel.startConversationInBrowser()
    quitHelper.browserCapturePending = true
    quitHelper.conversationReply = ["result": [:]]
    check(!browserQuitModel.quit() && browserQuitModel.conversationCapture, "Quit waits for browser acknowledgement")
    quitHelper.browserCapturePending = false
    let modalDeadline = Date().addingTimeInterval(4)
    while !quitObserved && Date() < modalDeadline {
        RunLoop.current.run(mode: .modalPanel, before: min(modalDeadline, Date().addingTimeInterval(0.1)))
    }
    check(quitObserved && !browserQuitModel.conversationCapture && quitHelper.shutdowns == 1,
          "the AppKit termination run-loop mode polls the acknowledgement and completes Quit")
}
