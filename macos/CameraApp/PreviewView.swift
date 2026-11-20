import SwiftUI

struct PreviewView: View {
    @ObservedObject var preview: PreviewController
    let streaming: Bool
    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Live preview").font(.headline)
            ZStack {
                RoundedRectangle(cornerRadius: 10).fill(Color.black)
                if let image = preview.image {
                    Image(decorative: image, scale: 1).resizable().scaledToFit().padding(1)
                        .accessibilityLabel("Live camera preview")
                } else {
                    VStack(spacing: 10) {
                        Image(systemName: streaming ? "video" : "video.slash").font(.largeTitle)
                        Text(message).multilineTextAlignment(.center)
                    }.foregroundStyle(.white.opacity(0.85)).padding(20)
                }
            }.frame(height: streaming || preview.image != nil ? 240 : 110).clipped()
            HStack {
                Label(status, systemImage: preview.phase == .live ? "circle.fill" : "info.circle")
                    .foregroundStyle(preview.phase == .live ? Color.green : Color.secondary)
                    .accessibilityIdentifier("preview-status")
                Spacer()
                if streaming && preview.phase == .unavailable {
                    Button("Retry Preview", action: preview.retry)
                }
            }.font(.callout)
            Text("Shows the full camera view sent to viewers, without mirroring. Preview pauses when this panel is hidden; streaming continues.")
                .font(.caption).foregroundStyle(.secondary).fixedSize(horizontal: false, vertical: true)
        }
    }
    private var status: String {
        switch preview.phase {
        case .idle: return streaming ? "Preview paused" : "Camera stopped"
        case .connecting: return "Waiting for video…"
        case .live: return "Preview live"
        case .unavailable: return "Preview unavailable"
        }
    }
    private var message: String {
        if !streaming { return "Choose Start Camera to see your live preview." }
        return preview.phase == .unavailable ? "Preview is unavailable. Your camera stream can keep running." : "Waiting for video…"
    }
}
