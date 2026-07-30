import AppKit
import Foundation
import WebKit

private let environment = ProcessInfo.processInfo.environment
private let uiBaseURL = URL(string: environment["UNIDROP_UI_URL"] ?? "http://127.0.0.1:43337")!
private let peerListenAddress = environment["UNIDROP_LISTEN_ADDRESS"] ?? ":43338"

private struct CoreInfo: Decodable {
    let id: String
    let name: String
    let peerPort: Int

    enum CodingKeys: String, CodingKey {
        case id, name
        case peerPort = "peer_port"
    }
}

private struct CoreSummary: Decodable {
    let nearby: Int
    let pending: Int
    let receiveMode: String
    let discoveryStatus: String

    enum CodingKeys: String, CodingKey {
        case nearby, pending
        case receiveMode = "receive_mode"
        case discoveryStatus = "discovery_status"
    }
}

final class UniDropAppDelegate: NSObject, NSApplicationDelegate, WKScriptMessageHandler,
    NetServiceBrowserDelegate, NetServiceDelegate {
    private var statusItem: NSStatusItem!
    private let popover = NSPopover()
    private var webView: WKWebView!
    private var refreshTimer: Timer?
    private var coreProcess: Process?
    private var coreInputPipe: Pipe?
    private var lastCoreLaunch = Date.distantPast
    private var webLoaded = false
    private var shuttingDown = false

    private let serviceBrowser = NetServiceBrowser()
    private var publishedService: NetService?
    private var resolvingServices: [NetService] = []
    private var bonjourStarted = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        configureStatusItem()
        configurePopover()
        serviceBrowser.delegate = self
        launchCoreIfNeeded()
        refreshStatus()
        refreshTimer = Timer.scheduledTimer(withTimeInterval: 2.0, repeats: true) { [weak self] _ in
            self?.refreshStatus()
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        refreshTimer?.invalidate()
        serviceBrowser.stop()
        publishedService?.stop()
        if !shuttingDown {
            coreInputPipe?.fileHandleForWriting.closeFile()
            coreProcess?.terminate()
        }
    }

    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool {
        togglePopover(nil)
        return true
    }

    private func configureStatusItem() {
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        guard let button = statusItem.button else { return }
        let image = NSImage(systemSymbolName: "arrow.up.arrow.down.circle.fill", accessibilityDescription: "UniDrop")
        image?.isTemplate = true
        button.image = image
        button.imagePosition = .imageLeading
        button.target = self
        button.action = #selector(togglePopover(_:))
        button.toolTip = "UniDrop is starting"
        statusItem.autosaveName = "com.unidrop.status-item"
    }

    private func configurePopover() {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        configuration.userContentController.add(self, name: "unidrop")
        webView = WKWebView(frame: NSRect(x: 0, y: 0, width: 410, height: 640), configuration: configuration)
        webView.loadHTMLString(Self.loadingHTML, baseURL: nil)

        let controller = NSViewController()
        controller.view = webView
        controller.preferredContentSize = NSSize(width: 410, height: 640)
        popover.contentViewController = controller
        popover.contentSize = NSSize(width: 410, height: 640)
        popover.behavior = .transient
        popover.animates = true
    }

    @objc private func togglePopover(_ sender: Any?) {
        guard let button = statusItem.button else { return }
        if popover.isShown {
            popover.performClose(sender)
        } else {
            if webLoaded {
                webView.reload()
            }
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            NSApp.activate(ignoringOtherApps: true)
        }
    }

    private func refreshStatus() {
        fetch("/api/summary", as: CoreSummary.self) { [weak self] summary in
            guard let self else { return }
            guard let summary else {
                self.showUnavailableState()
                self.launchCoreIfNeeded()
                return
            }
            let badge = summary.pending > 0 ? summary.pending : summary.nearby
            self.statusItem.button?.title = badge > 0 ? " \(badge)" : ""
            if summary.pending > 0 {
                self.statusItem.button?.toolTip = "\(summary.pending) incoming UniDrop request\(summary.pending == 1 ? "" : "s")"
            } else if summary.nearby > 0 {
                self.statusItem.button?.toolTip = "UniDrop • \(summary.nearby) nearby device\(summary.nearby == 1 ? "" : "s")"
            } else {
                self.statusItem.button?.toolTip = "UniDrop • searching nearby"
            }
            if !self.webLoaded {
                self.loadCompactInterface()
            }
            if !self.bonjourStarted {
                self.startBonjourDiscovery()
            }
        }
    }

    private func showUnavailableState() {
        statusItem.button?.title = ""
        statusItem.button?.toolTip = "UniDrop service is reconnecting"
        if webLoaded {
            webLoaded = false
            webView.loadHTMLString(Self.loadingHTML, baseURL: nil)
        }
    }

    private func loadCompactInterface() {
        var components = URLComponents(url: uiBaseURL, resolvingAgainstBaseURL: false)!
        components.path = "/"
        components.queryItems = [URLQueryItem(name: "compact", value: "1")]
        webView.load(URLRequest(url: components.url!))
        webLoaded = true
    }

    private func launchCoreIfNeeded() {
        if coreProcess?.isRunning == true || Date().timeIntervalSince(lastCoreLaunch) < 3.0 {
            return
        }
        guard let coreURL = Bundle.main.resourceURL?.appendingPathComponent("unidrop-core"),
              FileManager.default.isExecutableFile(atPath: coreURL.path) else {
            return
        }
        lastCoreLaunch = Date()
        let process = Process()
        process.executableURL = coreURL
        let uiHost = uiBaseURL.host ?? "127.0.0.1"
        let uiPort = uiBaseURL.port ?? 43337
        process.arguments = ["--no-open", "--exit-on-stdin-close", "--ui", "\(uiHost):\(uiPort)", "--listen", peerListenAddress]
        let inputPipe = Pipe()
        process.standardInput = inputPipe
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        do {
            try process.run()
            inputPipe.fileHandleForReading.closeFile()
            coreProcess = process
            coreInputPipe = inputPipe
        } catch {
            statusItem.button?.toolTip = "UniDrop could not start its secure service"
        }
    }

    private func startBonjourDiscovery() {
        fetch("/api/info", as: CoreInfo.self) { [weak self] info in
            guard let self, let info, !self.bonjourStarted else { return }
            self.bonjourStarted = true
            let serviceName = "UniDrop-\(info.id)"
            let service = NetService(domain: "local.", type: "_unidrop._tcp.", name: serviceName, port: Int32(info.peerPort))
            service.delegate = self
            let record = [
                "id": info.id.data(using: .utf8)!,
                "name": info.name.data(using: .utf8)!
            ]
            service.setTXTRecord(NetService.data(fromTXTRecord: record))
            service.publish(options: .noAutoRename)
            self.publishedService = service
            self.serviceBrowser.searchForServices(ofType: "_unidrop._tcp.", inDomain: "local.")
        }
    }

    func netServiceBrowser(_ browser: NetServiceBrowser, didFind service: NetService, moreComing: Bool) {
        if service.name == publishedService?.name { return }
        service.delegate = self
        resolvingServices.append(service)
        service.resolve(withTimeout: 6.0)
    }

    func netServiceDidResolveAddress(_ sender: NetService) {
        defer { releaseService(sender) }
        guard var host = sender.hostName, sender.port > 0 else { return }
        host = host.trimmingCharacters(in: CharacterSet(charactersIn: "."))
        addDiscoveredAddress("\(host):\(sender.port)")
    }

    func netService(_ sender: NetService, didNotResolve errorDict: [String: NSNumber]) {
        releaseService(sender)
    }

    private func releaseService(_ service: NetService) {
        resolvingServices.removeAll { $0 === service }
    }

    private func addDiscoveredAddress(_ address: String) {
        var request = URLRequest(url: uiBaseURL.appendingPathComponent("api/add-peer"))
        request.httpMethod = "POST"
        request.setValue("1", forHTTPHeaderField: "X-UniDrop-UI")
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try? JSONSerialization.data(withJSONObject: ["address": address])
        URLSession.shared.dataTask(with: request).resume()
    }

    private func fetch<T: Decodable>(_ path: String, as type: T.Type, completion: @escaping (T?) -> Void) {
        let request = URLRequest(url: uiBaseURL.appendingPathComponent(path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))))
        URLSession.shared.dataTask(with: request) { data, response, _ in
            let http = response as? HTTPURLResponse
            let value = data.flatMap { try? JSONDecoder().decode(T.self, from: $0) }
            DispatchQueue.main.async {
                completion(http?.statusCode == 200 ? value : nil)
            }
        }.resume()
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.frameInfo.request.url?.host == uiBaseURL.host,
              message.frameInfo.request.url?.port == uiBaseURL.port,
              let action = message.body as? String else { return }
        switch action {
        case "open":
            NSWorkspace.shared.open(uiBaseURL)
            popover.performClose(nil)
        case "quit":
            quitUniDrop()
        default:
            break
        }
    }

    private func quitUniDrop() {
        shuttingDown = true
        refreshTimer?.invalidate()
        serviceBrowser.stop()
        publishedService?.stop()
        coreInputPipe?.fileHandleForWriting.closeFile()
        coreProcess?.terminate()

        let launchctl = Process()
        launchctl.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        launchctl.arguments = ["bootout", "gui/\(getuid())/com.unidrop.app"]
        try? launchctl.run()
        DispatchQueue.main.asyncAfter(deadline: .now() + 0.4) {
            NSApp.terminate(nil)
        }
    }

    private static let loadingHTML = """
    <!doctype html><html><head><meta name="color-scheme" content="dark"><style>
    *{box-sizing:border-box}body{margin:0;height:100vh;display:grid;place-items:center;background:#020304;color:#f5f5f7;font:14px -apple-system,BlinkMacSystemFont,sans-serif}
    div{text-align:center}.mark{width:42px;height:42px;margin:0 auto 14px;border-radius:14px;display:grid;place-items:center;background:linear-gradient(145deg,#4f66ff,#9a5cff);font-size:22px;box-shadow:0 12px 35px #675dff44}.muted{color:#858995;margin-top:5px}
    </style></head><body><div><div class="mark">⇄</div><strong>Starting UniDrop</strong><div class="muted">Preparing secure local discovery…</div></div></body></html>
    """
}

let application = NSApplication.shared
let applicationDelegate = UniDropAppDelegate()
application.delegate = applicationDelegate
application.run()
