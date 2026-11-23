import SwiftUI
import WebKit

/// The call page on the stage. The surface owns the web view and its
/// lifetime; this only places it.
struct CallPageView: NSViewRepresentable {
    let surface: ConversationMediaSurface

    func makeNSView(context: Context) -> WKWebView {
        surface.loadPrejoin()
        return surface.view
    }

    func updateNSView(_ view: WKWebView, context: Context) {}
}

/// Join…: paste the invitation link the host sent. The field shows what you
/// pasted and names the server that issued it. While the camera streams,
/// the button says that joining stops the stream.
struct JoinCallView: View {
    @ObservedObject var model: CameraModel
    let dismiss: () -> Void
    // Kept only while the sheet is open, like the copied invitation.
    @State private var invitation = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Join a call").font(.headline)
            Text("Paste the invitation link the host sent you.").foregroundStyle(.secondary)
            TextField("Invitation link", text: $invitation, prompt: Text("Invitation link"))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .focused($focused)
                .onSubmit(join)
                .accessibilityLabel("Invitation link")
                .accessibilityIdentifier("conversation-invitation")
            if let host = Self.invitationHost(invitation) {
                Label("Invitation from \(host)", systemImage: "link").font(.callout).foregroundStyle(.secondary)
                    .accessibilityIdentifier("invitation-host")
            }
            if stopsStream {
                Label("Your camera can stream or be in a call, not both. Joining stops the stream.", systemImage: "info.circle")
                    .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            if let reason = model.conversationBlockedReason {
                Label(reason, systemImage: "info.circle").font(.callout).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack {
                Spacer()
                Button("Cancel", action: dismiss).keyboardShortcut(.cancelAction)
                Button(stopsStream ? "Stop Stream and Join" : "Join Call", action: join)
                    .buttonStyle(.borderedProminent).keyboardShortcut(.defaultAction).disabled(!canJoin)
                    .accessibilityIdentifier("join-call")
            }
        }
        .padding(20)
        .frame(width: 440)
        .onAppear { focused = true }
    }

    private var trimmed: String { invitation.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var stopsStream: Bool { model.canStop }
    private var canJoin: Bool { model.canStartConversation && !trimmed.isEmpty }

    private func join() {
        guard canJoin else { return }
        model.joinCall(trimmed)
        dismiss()
    }

    /// The server that issued a pasted invitation.
    static func invitationHost(_ text: String) -> String? {
        guard let url = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)) else { return nil }
        if url.scheme == "https", let host = url.host, url.path.hasPrefix("/r/") { return host }
        if url.scheme == "camstream", url.host == "conversation",
           let origin = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "origin" })?.value,
           let server = URL(string: origin), server.scheme == "https", let host = server.host { return host }
        return nil
    }
}
