import SwiftUI

// Routine sharing first, then the one-time Sharing settings. Sharing is video
// only: there are no microphone, sound, quality or per-share provider controls.
struct SharingView: View {
    @ObservedObject var sharing: SharingModel
    @State private var showOptions = false
    @State private var showAdvanced = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            shareCard
            settingsSection
        }.padding(.vertical, 8)
    }

    private var shareCard: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                HStack(alignment: .top, spacing: 12) {
                    Image(systemName: symbol).font(.system(size: 22)).foregroundStyle(tint).accessibilityHidden(true)
                    VStack(alignment: .leading, spacing: 3) {
                        Text(sharing.headline).font(.headline).accessibilityIdentifier("sharing-status")
                        if let viewers = sharing.viewerLine {
                            Text(viewers).font(.callout).accessibilityIdentifier("sharing-viewers")
                        }
                        if let media = sharing.mediaLabel {
                            Text(media).font(.callout).foregroundStyle(sharing.phase == .degraded ? Color.orange : Color.secondary)
                                .accessibilityIdentifier("sharing-media")
                        }
                        if let detail = sharing.detail {
                            Text(detail).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    Spacer()
                    VStack(alignment: .trailing, spacing: 4) {
                        // The audience badge sets a link apart from the private stream.
                        if let badge = sharing.scopeBadge {
                            Label(badge.title, systemImage: badge.symbol).font(.caption.weight(.semibold))
                                .accessibilityIdentifier("sharing-scope")
                        }
                        Label("Video only", systemImage: "video").font(.caption).foregroundStyle(.secondary)
                            .accessibilityLabel("Video only. Guests receive no audio.").accessibilityIdentifier("sharing-video-only")
                    }
                }
                if sharing.showsLinkControls {
                    Text(sharing.audienceNotice).font(.callout).fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("sharing-audience")
                    HStack {
                        Button(sharing.copying ? "Copying…" : "Copy Link", action: sharing.copyLink)
                            .buttonStyle(.borderedProminent).disabled(!sharing.canCopyLink)
                            .accessibilityIdentifier("copy-share-link")
                        Button("Stop Sharing", action: sharing.stopSharing).disabled(!sharing.canStopSharing)
                            .accessibilityHint("Ends the link and every viewer's video. The camera keeps running.")
                            .accessibilityIdentifier("stop-sharing")
                    }
                    Text("Stop Sharing ends this link for every viewer. The camera and your private stream keep running.")
                        .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                } else {
                    HStack {
                        Button(sharing.phase == .pending ? "Creating Link…" : sharing.shareButtonTitle, action: sharing.shareCamera)
                            .buttonStyle(.borderedProminent).disabled(!sharing.canShare)
                            .accessibilityHint(sharing.shareBlockedReason ?? "Creates a new viewing link for this camera.")
                            .accessibilityIdentifier("share-camera")
                        if sharing.phase == .pending { ProgressView().controlSize(.small) }
                    }
                    if sharing.supported {
                        DisclosureGroup("Options", isExpanded: $showOptions) {
                            VStack(alignment: .leading, spacing: 8) {
                                Picker("Link expires after", selection: $sharing.duration) {
                                    Text("15 minutes").tag(SharingDuration.fifteenMinutes)
                                    Text("1 hour").tag(SharingDuration.oneHour)
                                    Text("8 hours").tag(SharingDuration.eightHours)
                                }.accessibilityIdentifier("sharing-duration")
                                Picker("Viewers at a time", selection: $sharing.viewerLimit) {
                                    ForEach(1...3, id: \.self) { Text("\($0)").tag($0) }
                                }.accessibilityIdentifier("sharing-viewer-limit")
                            }.padding(.top, 6).frame(maxWidth: 320, alignment: .leading)
                        }.disabled(!sharing.canEditOptions)
                        Text(sharing.preShareNotice).font(.caption).foregroundStyle(.secondary)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                if let problem = sharing.problem {
                    Label(problem, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                        .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("sharing-error")
                }
                if let notice = sharing.notice {
                    Text(notice).font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("sharing-notice")
                }
            }.padding(8)
        }
    }

    private var settingsSection: some View {
        GroupBox {
            VStack(alignment: .leading, spacing: 12) {
                HStack {
                    Text("Sharing settings").font(.headline)
                    Spacer()
                    Text(sharing.setupStatus).font(.callout)
                        .foregroundStyle(sharing.setupStatus == "Ready" ? Color.green : sharing.setupStatus == "Needs attention" ? Color.orange : Color.secondary)
                        .accessibilityLabel("Sharing settings: \(sharing.setupStatus)").accessibilityIdentifier("sharing-setup-status")
                }
                if !sharing.supported {
                    Text(sharing.connected
                         ? "Sharing isn't available with this camera helper. Use a complete current Camstream Camera app."
                         : "Waiting for the camera helper.")
                        .font(.callout).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                } else {
                    guestPageStep
                    Divider()
                    relayStep
                    if let problem = sharing.setupProblem {
                        Label(problem, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange)
                            .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("sharing-setup-error")
                    }
                    HStack {
                        if sharing.setupWorking { ProgressView().controlSize(.small); Text("Working…").font(.callout) }
                        else if sharing.setupLockedByShare {
                            Text("Stop Sharing to change these settings.").font(.caption).foregroundStyle(.secondary)
                        } else if sharing.setupDirty, let validation = sharing.setupValidation {
                            Text(validation).font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        } else if let notice = sharing.setupNotice {
                            Text(notice).font(.caption).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Save Sharing Settings", action: sharing.saveSetup).disabled(!sharing.canSaveSetup)
                            .accessibilityIdentifier("save-sharing-settings")
                    }
                }
            }.padding(8)
        }
    }

    private var guestPageStep: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("1. Guest page").font(.subheadline.bold())
            Picker("Guest access", selection: $sharing.form.ingressMode) {
                ForEach(sharing.ingressOptions) { option in Text(option.label).tag(option.mode) }
            }.accessibilityIdentifier("sharing-ingress-mode").disabled(!sharing.canEditSetup)
            if sharing.registeredModes.isEmpty {
                Text("No guest access methods are available in this build.").font(.caption).foregroundStyle(.secondary)
            } else if sharing.form.ingressMode == "disabled" {
                Text("Sharing stays off until you choose how guests reach the viewing page.")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } else if sharing.form.ingressMode == "external_https" {
                LabeledContent("HTTPS address") {
                    TextField("https://camera.example.com", text: $sharing.form.publicBaseURL)
                        .labelsHidden().accessibilityLabel("HTTPS address").accessibilityIdentifier("sharing-public-url")
                }.disabled(!sharing.canEditSetup)
                Text("Route this address to \(SharingForm.trimmed(sharing.form.guestListen)) on this Mac. You manage the domain, TLS certificate and proxy; Camstream serves only the guest page.")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            } else {
                Text(sharing.selectedAudience == .privateTailnet
                     ? "Only devices on your tailnet can open the link."
                     : "Anyone with the link can open the viewing page from the internet.")
                    .font(.caption).foregroundStyle(.secondary)
            }
        }
    }

    private var relayStep: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("2. Connection relay").font(.subheadline.bold())
            Text("Viewers on restrictive networks connect through your relay. Sharing uses it automatically when a direct connection isn't possible.")
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            if sharing.form.relayMode == "cloudflare_turn" {
                LabeledContent("Key ID") {
                    TextField(sharing.credentialSaved ? "Saved" : "Key ID", text: $sharing.form.keyID)
                        .labelsHidden().accessibilityLabel("Cloudflare TURN key ID").accessibilityIdentifier("sharing-relay-key")
                }.disabled(!sharing.canEditSetup)
                LabeledContent("API token") {
                    SecureField(sharing.credentialSaved ? "Saved — enter a new token to replace it" : "API token", text: $sharing.form.apiToken)
                        .labelsHidden().accessibilityLabel("Cloudflare TURN API token").accessibilityIdentifier("sharing-relay-token")
                }.disabled(!sharing.canEditSetup)
                Text("Copy both values from the TURN key in your Cloudflare dashboard.")
                    .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 10) {
                Button(sharing.testingRelay ? "Testing…" : "Test Connection", action: sharing.testRelay)
                    .disabled(!sharing.canTestRelay).accessibilityIdentifier("test-relay")
                if sharing.testingRelay { ProgressView().controlSize(.small) }
                else if sharing.relayReady {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityHidden(true)
                } else if sharing.relayProblem != nil {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange).accessibilityHidden(true)
                }
                Text(sharing.relayStatus).font(.callout)
                    .foregroundStyle(sharing.relayProblem != nil ? Color.orange : Color.secondary)
                    .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("relay-status")
            }
            DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
                VStack(alignment: .leading, spacing: 10) {
                    Picker("Relay provider", selection: $sharing.form.relayMode) {
                        Text("Cloudflare TURN (recommended)").tag("cloudflare_turn")
                        Text("Self-hosted coturn").tag("coturn")
                        Text("No relay").tag("disabled")
                    }.accessibilityIdentifier("sharing-relay-mode")
                    if sharing.form.relayMode == "coturn" {
                        LabeledContent("TURN URLs") {
                            TextField("turns:relay.example.com:5349?transport=tcp", text: $sharing.form.relayURLs, axis: .vertical)
                                .lineLimit(1...4).labelsHidden().accessibilityLabel("TURN URLs, one per line")
                                .accessibilityIdentifier("sharing-relay-urls")
                        }
                        LabeledContent("Shared secret") {
                            SecureField(sharing.credentialSaved ? "Saved — enter a new secret to replace it" : "Shared secret", text: $sharing.form.sharedSecret)
                                .labelsHidden().accessibilityLabel("coturn shared secret").accessibilityIdentifier("sharing-relay-secret")
                        }
                    }
                    if sharing.form.ingressMode == "external_https" {
                        LabeledContent("Proxy target") {
                            TextField(SharingForm.defaultListener, text: $sharing.form.guestListen)
                                .labelsHidden().accessibilityLabel("Proxy target address on this Mac").accessibilityIdentifier("sharing-guest-listen")
                        }
                        Text("Your HTTPS proxy forwards guests to this address and port on this Mac.")
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }.padding(.top, 6)
            }.disabled(!sharing.canEditSetup)
        }
    }

    private var symbol: String {
        switch sharing.phase {
        case .active: return sharing.share?.media == .live ? "dot.radiowaves.left.and.right" : "link"
        case .degraded, .failed: return "exclamationmark.triangle"
        case .pending, .stopping: return "hourglass"
        case .ended: return "link.badge.plus"
        case .idle: return "link"
        }
    }
    private var tint: Color {
        switch sharing.phase {
        case .active: return sharing.share?.media == .live ? .green : .accentColor
        case .degraded, .failed: return .orange
        default: return .secondary
        }
    }
}
