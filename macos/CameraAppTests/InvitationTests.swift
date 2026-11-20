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
}
