import SwiftUI

/// Settings → Sharing: how guests reach the viewing page, and the relay for
/// viewers on restrictive networks. These involve credentials and a relay
/// test, so they keep an explicit Save. Sharing is video only: there are no
/// microphone, sound, quality or per-share provider controls.
struct SharingSettingsPane: View {
    @ObservedObject var sharing: SharingModel
    @State private var showAdvanced = false

    var body: some View {
        Form {
            Section {
                LabeledContent("Status") {
                    Text(sharing.setupStatus)
                        .foregroundStyle(sharing.setupStatus == "Ready" ? Color.green : sharing.setupStatus == "Needs attention" ? Color.orange : Color.secondary)
                        .accessibilityLabel("Sharing settings: \(sharing.setupStatus)").accessibilityIdentifier("sharing-setup-status")
                }
                if !sharing.supported {
                    Text(sharing.connected
                         ? "Sharing isn't available in this version of the camera service. Install a complete, current Camstream Camera to share."
                         : "Waiting for the camera service.")
                        .foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                }
            }
            if sharing.supported {
                guestPage
                relay
                Section {
                    if let problem = sharing.setupProblem {
                        FieldProblem(problem).accessibilityIdentifier("sharing-setup-error")
                    }
                    HStack {
                        if sharing.setupWorking { ProgressView().controlSize(.small); Text("Working…").foregroundStyle(.secondary) }
                        else if sharing.setupLockedByShare {
                            Text("Stop Sharing to change these settings.").foregroundStyle(.secondary)
                        } else if sharing.setupDirty, let validation = sharing.setupValidation {
                            Text(validation).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
                        } else if let notice = sharing.setupNotice {
                            Text(notice).foregroundStyle(.secondary)
                        }
                        Spacer()
                        Button("Save Sharing Settings", action: sharing.saveSetup).disabled(!sharing.canSaveSetup)
                            .accessibilityIdentifier("save-sharing-settings")
                    }
                }
            }
        }
    }

    private var guestPage: some View {
        Section {
            Picker("Guest access", selection: $sharing.form.ingressMode) {
                ForEach(sharing.ingressOptions) { option in Text(option.label).tag(option.mode) }
            }.accessibilityIdentifier("sharing-ingress-mode").disabled(!sharing.canEditSetup)
            if sharing.form.ingressMode == "external_https" {
                TextField("HTTPS address", text: $sharing.form.publicBaseURL, prompt: Text("https://camera.example.com"))
                    .accessibilityIdentifier("sharing-public-url").disabled(!sharing.canEditSetup)
            }
        } header: {
            Text("Guest page")
        } footer: {
            SettingsFootnote(guestFootnote)
        }
    }

    private var guestFootnote: String {
        if sharing.registeredModes.isEmpty { return "No guest access methods are available in this build." }
        switch sharing.form.ingressMode {
        case "disabled": return "Sharing stays off until you choose how guests reach the viewing page."
        case "external_https":
            return "Route this address to \(SharingForm.trimmed(sharing.form.guestListen)) on this Mac. You manage the domain, TLS certificate and proxy; Camstream serves only the guest page."
        default:
            return sharing.selectedAudience == .privateTailnet
                ? "Only devices on your tailnet can open the link."
                : "Anyone with the link can open the viewing page from the internet."
        }
    }

    private var relay: some View {
        Section {
            if sharing.form.relayMode == "cloudflare_turn" {
                TextField("Key ID", text: $sharing.form.keyID, prompt: Text(sharing.credentialSaved ? "Saved" : "Key ID"))
                    .accessibilityLabel("Cloudflare TURN key ID").accessibilityIdentifier("sharing-relay-key")
                    .disabled(!sharing.canEditSetup)
                SecureField("API token", text: $sharing.form.apiToken,
                            prompt: Text(sharing.credentialSaved ? "Saved — enter a new token to replace it" : "API token"))
                    .accessibilityLabel("Cloudflare TURN API token").accessibilityIdentifier("sharing-relay-token")
                    .disabled(!sharing.canEditSetup)
            }
            HStack(spacing: 10) {
                if sharing.testingRelay { ProgressView().controlSize(.small) }
                else if sharing.relayReady {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(.green).accessibilityHidden(true)
                } else if sharing.relayProblem != nil {
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(.orange).accessibilityHidden(true)
                }
                Text(sharing.relayStatus)
                    .foregroundStyle(sharing.relayProblem != nil ? Color.orange : Color.secondary)
                    .fixedSize(horizontal: false, vertical: true).accessibilityIdentifier("relay-status")
                Spacer()
                Button(sharing.testingRelay ? "Testing…" : "Test Connection", action: sharing.testRelay)
                    .disabled(!sharing.canTestRelay).accessibilityIdentifier("test-relay")
            }
            DisclosureGroup("Advanced", isExpanded: $showAdvanced) {
                Picker("Relay provider", selection: $sharing.form.relayMode) {
                    Text("Cloudflare TURN (recommended)").tag("cloudflare_turn")
                    Text("Self-hosted coturn").tag("coturn")
                    Text("No relay").tag("disabled")
                }.accessibilityIdentifier("sharing-relay-mode")
                if sharing.form.relayMode == "coturn" {
                    TextField("TURN URLs", text: $sharing.form.relayURLs, prompt: Text("turns:relay.example.com:5349?transport=tcp"), axis: .vertical)
                        .lineLimit(1...4).accessibilityLabel("TURN URLs, one per line")
                        .accessibilityIdentifier("sharing-relay-urls")
                    SecureField("Shared secret", text: $sharing.form.sharedSecret,
                                prompt: Text(sharing.credentialSaved ? "Saved — enter a new secret to replace it" : "Shared secret"))
                        .accessibilityLabel("coturn shared secret").accessibilityIdentifier("sharing-relay-secret")
                }
                if sharing.form.ingressMode == "external_https" {
                    TextField("Proxy target", text: $sharing.form.guestListen, prompt: Text(SharingForm.defaultListener))
                        .accessibilityLabel("Proxy target address on this Mac").accessibilityIdentifier("sharing-guest-listen")
                }
            }.disabled(!sharing.canEditSetup)
        } header: {
            HStack(spacing: 6) {
                Text("Connection relay")
                InfoButton(text: "Viewers on restrictive networks connect through your relay. Sharing uses it automatically when a direct connection isn't possible.",
                           label: "About the connection relay")
            }
        } footer: {
            SettingsFootnote(sharing.form.relayMode == "cloudflare_turn"
                ? "Copy both values from the TURN key in your Cloudflare dashboard."
                : sharing.form.ingressMode == "external_https" && showAdvanced
                    ? "Your HTTPS proxy forwards guests to the proxy target on this Mac."
                    : "Test the connection after changing relay settings.")
        }
    }
}
