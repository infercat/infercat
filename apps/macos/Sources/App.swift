import AppKit
import Combine
import SwiftUI

@main
enum InfercatApp {
    @MainActor static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        // An accessory app: the Dock icon appears only while the window is open.
        app.setActivationPolicy(.accessory)
        app.run()
        withExtendedLifetime(delegate) {}
    }
}

@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, NSPopoverDelegate {
    private var model: HostModel!
    private var statusItem: NSStatusItem?
    private let popover = NSPopover()
    private var window: NSWindow?
    private var watcher: AnyCancellable?
    private var lastTitle = ""

    func applicationDidFinishLaunching(_ notification: Notification) {
        Brand.registerFont()
        // Unit tests load this bundle as their host; they must not start a monitor.
        if NSClassFromString("XCTestCase") != nil { return }

        let arguments = CommandLine.arguments
        if let index = arguments.firstIndex(of: "--capture"), index + 1 < arguments.count {
            let status = Capture.run(into: URL(fileURLWithPath: arguments[index + 1])) ? 0 : 1
            exit(Int32(status))
        }

        // `--fixtures` is an explicit preview mode. The default is always the real CLI:
        // a missing or unreadable answer is an error, never a quiet fall back to demo data.
        model = HostModel(client: arguments.contains("--fixtures") ? FixtureCLI() : ProcessCLI())

        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        item.button?.target = self
        item.button?.action = #selector(togglePopover)
        statusItem = item

        popover.behavior = .transient
        popover.delegate = self
        popover.contentViewController = NSHostingController(
            rootView: PopoverView(model: model,
                                  openWindow: { [weak self] in self?.showWindow() },
                                  openConsole: { [weak self] in self?.openConsole() }))

        watcher = model.objectWillChange.sink { [weak self] _ in
            Task { @MainActor in self?.refreshStatusItem() }
        }
        installMenu()
        model.start()
        refreshStatusItem()
        showWindow()
    }

    func applicationWillTerminate(_ notification: Notification) {
        model?.stopMonitoring()
    }

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool { false }

    // MARK: - The menu-bar item (design spec §3)

    private func refreshStatusItem() {
        guard let button = statusItem?.button else { return }
        let title = model.menuBarTitle
        guard title != lastTitle || button.image == nil else { return }
        lastTitle = title

        let asleep = model.presence == .stopped
        let renderer = ImageRenderer(content: Loaf(asleep: asleep).frame(width: 18, height: 18))
        renderer.scale = 2
        let image = renderer.nsImage
        image?.isTemplate = true
        button.image = image
        button.imagePosition = .imageLeading
        // States 1 and 2 are the dimmed loaf; 3–6 are full strength.
        button.alphaValue = (model.presence == .stopped || model.presence == .starting) ? 0.42 : 1

        let text = NSMutableAttributedString()
        if let badge = badgeColour {
            let mark = NSMutableAttributedString(string: " ■")
            mark.addAttributes([.foregroundColor: badge,
                                .font: NSFont.systemFont(ofSize: 8)],
                               range: NSRange(location: 1, length: 1))
            text.append(mark)
        }
        if model.connectedCount > 0, model.presence != .stopped, model.presence != .starting {
            let count = NSAttributedString(
                string: " \(model.connectedCount)",
                attributes: [.font: NSFont(name: "IBMPlexMono", size: 11)
                                ?? .monospacedDigitSystemFont(ofSize: 11, weight: .regular),
                             .foregroundColor: NSColor.labelColor])
            text.append(count)
        }
        button.attributedTitle = text
        button.toolTip = title
        button.setAccessibilityLabel(title)
    }

    private var badgeColour: NSColor? {
        switch model.presence {
        case .engineOffline: NSColor.systemRed
        case .needsAttention: NSColor.systemYellow
        default: nil
        }
    }

    @objc private func togglePopover() {
        if popover.isShown {
            popover.performClose(nil)
        } else if let button = statusItem?.button {
            popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
            updateVisibility()
        }
    }

    func popoverDidClose(_ notification: Notification) { updateVisibility() }

    // MARK: - The window

    @objc private func showWindow() {
        popover.performClose(nil)
        if window == nil {
            let root = MainWindow(model: model, openConsole: { [weak self] in self?.openConsole() })
            let created = NSWindow(contentViewController: NSHostingController(rootView: root))
            created.title = model.text("win_title")
            created.styleMask = [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView]
            created.setContentSize(NSSize(width: 900, height: 600))
            created.isReleasedWhenClosed = false
            created.delegate = self
            created.center()
            window = created
        }
        NSApp.setActivationPolicy(.regular)
        window?.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        updateVisibility()
    }

    func windowWillClose(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        Task { @MainActor in self.updateVisibility() }
    }

    /// 2 s while something is on screen, 10 s otherwise (design spec §4).
    private func updateVisibility() {
        model.setVisible(window?.isVisible == true || popover.isShown)
    }

    private func openConsole() {
        Task { await model.openConsole() }
    }

    // MARK: - Main menu

    private func installMenu() {
        let bar = NSMenu()

        let appItem = NSMenuItem()
        let appMenu = NSMenu(title: "Infercat")
        appMenu.addItem(target(NSMenuItem(title: model.text("menu_about"), action: #selector(about), keyEquivalent: "")))
        appMenu.addItem(.separator())
        appMenu.addItem(target(NSMenuItem(title: model.text("act_open"), action: #selector(showWindow), keyEquivalent: "0")))
        appMenu.addItem(target(NSMenuItem(title: model.text("menu_refresh"), action: #selector(refresh), keyEquivalent: "r")))
        appMenu.addItem(.separator())
        let quit = NSMenuItem(title: model.text("act_quit"), action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        quit.toolTip = model.text("quit_note")
        appMenu.addItem(quit)
        appItem.submenu = appMenu
        bar.addItem(appItem)

        let editItem = NSMenuItem()
        let editMenu = NSMenu(title: "Edit")
        editMenu.addItem(NSMenuItem(title: "Copy", action: #selector(NSText.copy(_:)), keyEquivalent: "c"))
        editMenu.addItem(NSMenuItem(title: "Paste", action: #selector(NSText.paste(_:)), keyEquivalent: "v"))
        editMenu.addItem(NSMenuItem(title: "Select All", action: #selector(NSText.selectAll(_:)), keyEquivalent: "a"))
        editItem.submenu = editMenu
        bar.addItem(editItem)

        let windowItem = NSMenuItem()
        let windowMenu = NSMenu(title: model.text("menu_window"))
        windowMenu.addItem(NSMenuItem(title: model.text("menu_close"), action: #selector(NSWindow.performClose(_:)), keyEquivalent: "w"))
        windowItem.submenu = windowMenu
        bar.addItem(windowItem)

        NSApp.mainMenu = bar
    }

    private func target(_ item: NSMenuItem) -> NSMenuItem {
        item.target = self
        return item
    }

    @objc private func refresh() { model.refreshNow() }

    @objc private func about() {
        let credits = NSAttributedString(string: model.text("about_credits"))
        NSApp.orderFrontStandardAboutPanel(options: [.credits: credits])
    }
}
