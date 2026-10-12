// Artifact-level ABI check, part 3: the screen-state PUBLISHING surface.
//
// This mirrors Library/Network/ScreenStateObserver.swift as
// docs/fork/apple-screen-state-observer.patch carries it, compiled against the
// Libbox.xcframework that was just built from this core. It proves three things the
// design doc asserts and nothing else checks:
//
//   1. `import notify` resolves and notify_register_dispatch / notify_get_state /
//      notify_cancel have the Swift signatures the observer assumes (Int32 token,
//      DispatchQueue, UInt64 out-state);
//   2. LibboxCommandServer really exposes recordScreenState(_: Bool),
//      recordLockState(_: Bool), reconcileScreenState(_: Bool) and
//      reconcileLockState(_: Bool) with those exact types in the shipped artifact;
//   3. the file is iOS-only, so a macOS build of this same translation unit compiles
//      it away and never reaches the private notification names.
//
// # Why this mirrors protocols rather than the whole file
//
// The pinned ScreenStateObserver.swift is no longer a small `#if os(iOS)` observer: it is a
// full implementation built on two protocols, `DarwinNotifyAPI` (so the notify failure
// paths can be driven by a fake) and `ScreenStatePublishing` (a one-to-one mirror of the
// LibboxCommandServer record methods). Those two protocol declarations are what a probe
// can check without dragging in the whole policy layer, and they are the text the
// provenance gate anchors this witness to.
//
// The declarations below are verbatim from the pinned file. The adapter at the end is the
// point of the exercise: a protocol is only meaningful if the shipped framework can
// satisfy it, so if Libbox ever changes the type or the label of one of these methods,
// this file stops compiling.
#if os(iOS)
    import Dispatch
    import Foundation
    import Libbox
    import notify

    /// One `notify_get_state` attempt. Verbatim from the pinned ScreenStateObserver.swift.
    enum NotifyStateRead: Equatable {
        case value(UInt64)
        case failed(status: UInt32)
    }

    /// One `notify_register_dispatch` attempt. Verbatim from the pinned file.
    enum NotifyRegistration: Equatable {
        case registered(token: Int32)
        case failed(status: UInt32)

        var token: Int32? {
            guard case let .registered(token) = self else {
                return nil
            }
            return token
        }
    }

    /// One `notify_cancel` attempt. Verbatim from the pinned file.
    enum NotifyOutcome: Equatable {
        case ok
        case failed(status: UInt32)
    }

    /// The three Darwin `notify` entry points the observer uses, narrowed to what it needs.
    /// Verbatim from the pinned file. This is what proves (1): the C signatures the observer
    /// assumes are the ones the SDK actually declares, because `SystemNotifyAPI` below has
    /// to call them with these types.
    protocol DarwinNotifyAPI: AnyObject {
        func registerDispatch(name: String,
                              queue: DispatchQueue,
                              handler: @escaping (Int32) -> Void) -> NotifyRegistration
        func getState(token: Int32) -> NotifyStateRead
        func cancel(token: Int32) -> NotifyOutcome
    }

    /// What the observer is allowed to tell the core. Verbatim from the pinned file, minus
    /// the commentary. This is what proves (2) together with the adapter below.
    protocol ScreenStatePublishing: AnyObject {
        /// EDGE plus LEVEL: the system witnessed this transition, so a new sleep begins.
        func recordScreenState(on: Bool)
        /// EDGE plus LEVEL. `false` is the only fact that releases the level.
        func recordLockState(locked: Bool)
        /// LEVEL only: a current-state read found the display in this state.
        func reconcileScreenState(on: Bool)
        /// LEVEL only: a current-state read found the device in this lock state.
        func reconcileLockState(locked: Bool)
    }

    /// Calls the real C entry points, so the Swift signatures in `DarwinNotifyAPI` are
    /// checked against the SDK rather than asserted.
    final class SystemNotifyAPI: DarwinNotifyAPI {
        func registerDispatch(name: String,
                              queue: DispatchQueue,
                              handler: @escaping (Int32) -> Void) -> NotifyRegistration {
            var token: Int32 = -1
            let status = notify_register_dispatch(name, &token, queue, { token in handler(token) })
            return status == NOTIFY_STATUS_OK ? .registered(token: token) : .failed(status: status)
        }

        func getState(token: Int32) -> NotifyStateRead {
            var state: UInt64 = 0
            let status = notify_get_state(token, &state)
            return status == NOTIFY_STATUS_OK ? .value(state) : .failed(status: status)
        }

        func cancel(token: Int32) -> NotifyOutcome {
            let status = notify_cancel(token)
            return status == NOTIFY_STATUS_OK ? .ok : .failed(status: status)
        }
    }

    /// The adapter that makes `ScreenStatePublishing` a claim about the SHIPPED framework
    /// rather than about this file: every method forwards to `LibboxCommandServer`, so a
    /// renamed method, a changed argument label or a changed type is a compile error here.
    final class LibboxScreenStatePublisher: ScreenStatePublishing {
        private let commandServer: LibboxCommandServer

        init(commandServer: LibboxCommandServer) {
            self.commandServer = commandServer
        }

        func recordScreenState(on: Bool) {
            commandServer.recordScreenState(on)
        }

        func recordLockState(locked: Bool) {
            commandServer.recordLockState(locked)
        }

        func reconcileScreenState(on: Bool) {
            commandServer.reconcileScreenState(on)
        }

        func reconcileLockState(locked: Bool) {
            commandServer.reconcileLockState(locked)
        }
    }
#endif
