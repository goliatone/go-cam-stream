import SwiftUI
import WebKit

struct ConversationView: View {
    @ObservedObject var model: CameraModel
    @State private var invitation = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            if let surface = model.conversationSurface {
                // The call page offers Copy Invitation in its waiting tile; the Call menu keeps it too.
                if model.capture.active {
                    HStack {
                        Text("Stop the camera stream to use call devices.").font(.callout).foregroundStyle(.secondary)
                        Button("Stop Camera Stream", action: model.stop).disabled(!model.canStop)
                    }
                }
                ConversationWebView(surface: surface).id(ObjectIdentifier(surface))
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .accessibilityIdentifier("conversation-media")
            } else if model.conversationPrejoin?.admission.role == .host {
                if model.hostInBrowser {
                    browserHost
                } else {
                    HStack {
                        if model.canCopyConversation { Button("Copy Invitation", action: model.copyConversationInvitation) }
                        Button("Join Here", action: model.joinConversationHere).buttonStyle(.borderedProminent)
                        Button("Open Host in Browser", action: model.hostConversationInBrowser)
                        Spacer()
                        Button("End Conversation", action: model.closeConversation)
                    }
                }
                Spacer()
            } else {
                if let feedback = model.conversationFeedback {
                    FeedbackCard(feedback: feedback, perform: model.perform, identifier: "conversation-error")
                }
                GroupBox("Start or join a conversation") {
                    VStack(alignment: .leading, spacing: 12) {
                        HStack {
                            Button("Start Conversation", action: model.startConversation).buttonStyle(.borderedProminent)
                                .help(model.conversationBlockedReason ?? "Start a call with your camera and microphone off.")
                                .accessibilityHint(model.conversationBlockedReason ?? "")
                            Menu("More start options") {
                                Button("Set Up Devices First", action: model.prepareConversation)
                                Button("Host in Browser", action: model.startConversationInBrowser)
                            }.fixedSize()
                        }.disabled(!canStart)
                        Text(model.helperBlockedReason ?? "Your camera and microphone start off.")
                            .foregroundStyle(.secondary)
                        Text("To join, paste the invitation link from the host.")
                            .foregroundStyle(.secondary)
                        HStack {
                            SecureField("Conversation invitation", text: $invitation)
                                .accessibilityIdentifier("conversation-invitation")
                                .onSubmit(open)
                            Button("Open Invitation", action: open)
                                .disabled(!canOpen)
                                .help(openBlockedReason ?? "Open the conversation from this invitation.")
                                .accessibilityHint(openBlockedReason ?? "")
                        }
                        if let host = Self.invitationHost(invitation) {
                            Label("Invitation from \(host)", systemImage: "link").font(.callout).foregroundStyle(.secondary)
                                .accessibilityIdentifier("invitation-host")
                        }
                    }.padding(8)
                }
                if model.openingConversation { ProgressView("Opening conversation…").controlSize(.small) }
                Spacer()
            }
            // The chooser shows problems in the recovery card; elsewhere they stay inline.
            if let notice = model.conversationNotice, !(notice.problem && chooser && model.conversationFeedback != nil) {
                if notice.problem {
                    Label(notice.text, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("conversation-status")
                } else {
                    Text(notice.text).foregroundStyle(.secondary).accessibilityIdentifier("conversation-status")
                }
            }
        }
    }

    /// A room hosted in the browser: its state from the owner's status, with
    /// Copy Invitation as the primary action until the guest arrives.
    private var browserHost: some View {
        let phase = model.conversationActivity?.phase
        let waiting = phase != .connecting && phase != .connected && phase != .reconnecting
        return VStack(alignment: .leading, spacing: 10) {
            Label(Self.browserStatus(phase), systemImage: "safari").font(.headline)
                .accessibilityIdentifier("browser-room")
            Text("The host controls are open in your browser. Turn on your camera and mic there.")
                .foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            HStack {
                if model.canCopyConversation {
                    if waiting {
                        Button("Copy Invitation", action: model.copyConversationInvitation).buttonStyle(.borderedProminent)
                    } else {
                        Button("Copy Invitation", action: model.copyConversationInvitation)
                    }
                }
                Spacer()
                Button("End Conversation", action: model.closeConversation)
            }
        }
    }

    static func browserStatus(_ phase: ConversationActivity.Phase?) -> String {
        switch phase {
        case .waiting?: return "Waiting for your guest"
        case .connecting?: return "Your guest is joining"
        case .connected?: return "Your guest is connected"
        case .reconnecting?: return "Reconnecting to your guest"
        default: return "Hosting in your browser"
        }
    }

    /// The server that issued a pasted invitation. The secret part stays masked.
    static func invitationHost(_ text: String) -> String? {
        guard let url = URL(string: text.trimmingCharacters(in: .whitespacesAndNewlines)) else { return nil }
        if url.scheme == "https", let host = url.host, url.path.hasPrefix("/r/") { return host }
        if url.scheme == "camstream", url.host == "conversation",
           let origin = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first(where: { $0.name == "origin" })?.value,
           let server = URL(string: origin), server.scheme == "https", let host = server.host { return host }
        return nil
    }

    private var canStart: Bool { model.canStartConversation }
    private var chooser: Bool { model.conversationSurface == nil && model.conversationPrejoin == nil }

    private var canOpen: Bool { canStart && !invitation.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    private var openBlockedReason: String? {
        if let reason = model.conversationBlockedReason { return reason }
        return canOpen ? nil : "Paste an invitation link first."
    }

    private func open() {
        guard canOpen else { return }
        model.openConversationInvitation(invitation.trimmingCharacters(in: .whitespacesAndNewlines))
        invitation = ""
    }
}

private struct ConversationWebView: NSViewRepresentable {
    let surface: ConversationMediaSurface

    func makeNSView(context: Context) -> WKWebView {
        surface.loadPrejoin()
        return surface.view
    }

    func updateNSView(_ view: WKWebView, context: Context) {}
}
