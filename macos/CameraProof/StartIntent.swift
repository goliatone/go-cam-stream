// Correlate asynchronous configure replies with the most recent explicit Start.
// Stop must revoke that intent even while device discovery is still pending.
struct StartIntent {
    private var requestID: String?

    mutating func begin(_ id: String?) { requestID = id }
    mutating func cancel() { requestID = nil }

    mutating func consume(_ id: String?, succeeded: Bool) -> Bool {
        guard let pending = requestID, id == pending else { return false }
        requestID = nil
        return succeeded
    }
}
