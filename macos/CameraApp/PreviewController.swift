import Foundation
import Combine
import CoreGraphics

final class PreviewController: ObservableObject {
    enum Phase { case idle, connecting, live, unavailable }
    typealias Request = (String, String?, @escaping ([String: Any]) -> Void) -> Void
    @Published private(set) var phase: Phase = .idle
    @Published private(set) var image: CGImage?
    var request: Request?
    private var wanted = false
    private var ready = false
    private var supported = false
    private var epoch = 0
    private var captureGeneration = -1
    private var activeEpoch = -1
    private var active: PreviewEndpoint?
    private var inFlight = false
    private var reader: PreviewReader?
    private var watchdog: Timer?

    func update(wanted: Bool, ready: Bool, supported: Bool, captureGeneration: Int) {
        if self.wanted != wanted || self.captureGeneration != captureGeneration {
            epoch += 1; self.captureGeneration = captureGeneration
            clear(); phase = .idle
        }
        self.wanted = wanted; self.ready = ready; self.supported = supported
        if !ready { epoch += 1; clear(); active = nil; phase = wanted ? .unavailable : .idle; return }
        reconcile()
    }
    func retry() {
        guard wanted, ready else { return }
        epoch += 1; clear(); phase = .idle; reconcile()
    }
    private func clear() { reader?.stop(); reader = nil; image = nil; watchdog?.invalidate(); watchdog = nil }
    private func reconcile() {
        guard ready, !inFlight, let request = request else { return }
        if let active = active, !wanted || activeEpoch != epoch || phase == .unavailable {
            clear(); inFlight = true
            request("preview_detach", active.generation) { [weak self] _ in
                guard let self = self else { return }
                self.inFlight = false; self.active = nil; self.reconcile()
            }
            return
        }
        guard wanted, active == nil, phase != .unavailable else { return }
        guard supported else { phase = .unavailable; return }
        let token = epoch
        phase = .connecting; inFlight = true
        request("preview_attach", nil) { [weak self] reply in
            guard let self = self else { return }
            self.inFlight = false
            guard let result = reply["result"], JSONSerialization.isValidJSONObject(result),
                  let data = try? JSONSerialization.data(withJSONObject: result),
                  let endpoint = try? JSONDecoder().decode(PreviewEndpoint.self, from: data), endpoint.identifier != nil else {
                if self.wanted && self.epoch == token { self.phase = .unavailable }
                self.reconcile(); return
            }
            self.active = endpoint; self.activeEpoch = token
            guard self.ready, self.wanted, self.epoch == token else { self.reconcile(); return }
            self.watchdog = Timer.scheduledTimer(withTimeInterval: 8, repeats: false) { [weak self] _ in self?.failed(token) }
            let reader = PreviewReader(onFrame: { [weak self] image in
                guard let self = self, self.wanted, self.epoch == token, self.phase != .unavailable else { return }
                self.image = image; self.phase = .live
                self.watchdog?.invalidate()
                self.watchdog = Timer.scheduledTimer(withTimeInterval: 5, repeats: false) { [weak self] _ in self?.failed(token) }
            }, onFailure: { [weak self] in self?.failed(token) })
            self.reader = reader; reader.start(endpoint)
        }
    }
    private func failed(_ token: Int) {
        guard wanted, ready, epoch == token else { return }
        clear(); phase = .unavailable; reconcile()
    }
}
