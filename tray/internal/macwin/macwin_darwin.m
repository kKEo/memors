// Windows for memors-tray: one NSWindow with a WKWebView per page (a
// memors-mcp server's pages, or one of the tray's own), living on the run
// loop fyne.io/systray already owns.
//
// The windows are shown as the tabs of one window: each new one joins the
// tab group of those already open (AppKit's window tabbing), so there is a
// single MemoRS Tray window on screen while every page keeps a web view,
// and with it a state, of its own. A tab the user drags out becomes a
// window again, as in any macOS app.
//
// Each window has a private, non-persistent website data store (the login
// cookie never reaches disk), content JavaScript off (the pages have none),
// and a navigation delegate that asks Go (webpolicy) about every navigation:
// the server's own pages load in the window, links elsewhere open in the
// default browser, the tray's own close link closes the window, everything
// else is refused.
//
// ⌘↩ sends the question on the Ask page, whose question box is a form like
// any other; this too is a script the tray evaluates itself.
//
// Closing a window (its Dismiss button, ⌘W or the close box) first asks
// whether the page has unsaved form changes. The check is a script the tray
// evaluates itself, which WebKit allows even with content JavaScript off,
// and a sheet names the changed fields and asks before they are thrown away.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include "macwin.h"

// Implemented in Go (macwin_darwin.go).
extern int macwinDecide(char *key, char *url, int clicked);
extern void macwinClosed(char *key);

enum { decideAllow = 0, decideExternal = 1, decideDeny = 2, decideClose = 3 };

// dirtyProbe lists the controls of the page's POST forms that differ from
// the value they loaded with, each by the name the page gives it: its
// data-label (shown as written), else its aria-label, its label's text or
// its field name (shown with a capital). Values are left out, so a typed key never reaches the sheet. An
// empty list means nothing to lose. GET forms (such as a search box) don't
// count, nor do controls marked data-nav, which only choose what the page
// shows (the tabs of Settings). A drop-down none of whose options is marked
// selected loads showing its first one, and that is no change.
static NSString *const dirtyProbe =
    @"(function(){"
     "const changed = [];"
     "const nameOf = (el) => {"
     "  const own = el.getAttribute('data-label');"
     "  let s = own || el.getAttribute('aria-label') ||"
     "    (el.labels && el.labels[0] && el.labels[0].textContent) || el.name || 'A field';"
     "  s = s.replace(/\\s+/g, ' ').trim().replace(/:$/, '');"
     "  if (s.length > 60) s = s.slice(0, 59) + '…';"
     "  return own ? s : s.charAt(0).toUpperCase() + s.slice(1);"
     "};"
     "const differs = (el) => {"
     "  const t = (el.type || '').toLowerCase();"
     "  if (t === 'checkbox' || t === 'radio') return el.checked !== el.defaultChecked;"
     "  if (el.tagName === 'SELECT') {"
     "    const opts = Array.from(el.options);"
     "    const was = opts.map((o) => o.defaultSelected);"
     "    if (!el.multiple && el.size <= 1 && opts.length && !was.includes(true)) {"
     "      const first = opts.findIndex((o) => !o.disabled);"
     "      was[first < 0 ? 0 : first] = true;"
     "    }"
     "    return opts.some((o, i) => o.selected !== was[i]);"
     "  }"
     "  return 'defaultValue' in el && el.value !== el.defaultValue;"
     "};"
     "for (const f of document.querySelectorAll('form')) {"
     "  if (f.method !== 'post') continue;"
     "  for (const el of f.elements) {"
     "    if (el.disabled || el.hasAttribute('data-nav')) continue;"
     "    const t = (el.type || '').toLowerCase();"
     "    if (t === 'hidden' || t === 'submit' || t === 'button' || t === 'reset') continue;"
     "    if (!differs(el)) continue;"
     "    const n = nameOf(el);"
     "    if (!changed.includes(n)) changed.push(n);"
     "  }"
     "}"
     "return changed;"
     "})()";

// maxChangesShown bounds the changes the sheet lists by name.
enum { maxChangesShown = 8 };

// changesText is what the sheet says about the changed controls.
static NSString *changesText(NSArray<NSString *> *changed) {
	NSMutableString *text = [NSMutableString stringWithString:@"Not saved yet:"];
	NSUInteger shown = MIN(changed.count, (NSUInteger)maxChangesShown);
	for (NSUInteger i = 0; i < shown; i++) {
		[text appendFormat:@"\n• %@", changed[i]];
	}
	if (changed.count > shown) {
		[text appendFormat:@"\n…and %lu more", (unsigned long)(changed.count - shown)];
	}
	return text;
}

// sendForm submits the page's question box, if it has one and the box is
// filled in as the form requires.
static NSString *const sendForm =
    @"(function(){"
     "const f = document.querySelector('form.composer');"
     "if (f && f.reportValidity()) f.submit();"
     "})()";

@interface MWWindow : NSObject <NSWindowDelegate, WKNavigationDelegate, WKUIDelegate>
@property (copy) NSString *key;
@property (strong) NSWindow *window;
@property (strong) WKWebView *web;
@property BOOL closeConfirmed; // the unsaved-changes check has passed; let the next close through
@end

static NSMutableDictionary<NSString *, MWWindow *> *windows;
static BOOL debugWindows;

// tabGroup is the tabbing identifier every window carries, and frameName
// the name the one frame they share is saved under.
static NSString *const tabGroup = @"memors-tray";
static NSString *const frameName = @"memors-tray window";

static int decide(NSString *key, NSURL *url, BOOL clicked) {
	return macwinDecide((char *)key.UTF8String, (char *)(url.absoluteString ?: @"").UTF8String, clicked ? 1 : 0);
}

@implementation MWWindow

- (void)webView:(WKWebView *)webView
    decidePolicyForNavigationAction:(WKNavigationAction *)action
                    decisionHandler:(void (^)(WKNavigationActionPolicy))decisionHandler {
	NSURL *url = action.request.URL;
	int d = decide(self.key, url, action.navigationType == WKNavigationTypeLinkActivated);
	if (d == decideAllow) {
		decisionHandler(WKNavigationActionPolicyAllow);
		return;
	}
	decisionHandler(WKNavigationActionPolicyCancel);
	if (d == decideExternal) {
		[[NSWorkspace sharedWorkspace] openURL:url];
	} else if (d == decideClose) {
		[self.window performClose:nil];
	}
}

// Links with target=_blank: load the server's own pages here, open the
// rest in the browser, never create a second web view.
- (WKWebView *)webView:(WKWebView *)webView
    createWebViewWithConfiguration:(WKWebViewConfiguration *)configuration
               forNavigationAction:(WKNavigationAction *)action
                    windowFeatures:(WKWindowFeatures *)features {
	int d = decide(self.key, action.request.URL, YES);
	if (d == decideAllow) {
		[webView loadRequest:action.request];
	} else if (d == decideExternal) {
		[[NSWorkspace sharedWorkspace] openURL:action.request.URL];
	} else if (d == decideClose) {
		[self.window performClose:nil];
	}
	return nil;
}

// windowShouldClose runs for the Dismiss button, ⌘W and the close box. The
// answer needs the page, which only arrives asynchronously, so the close is
// refused here and finished from the completion handler.
- (BOOL)windowShouldClose:(NSWindow *)sender {
	if (self.closeConfirmed) {
		return YES;
	}
	__weak MWWindow *weakSelf = self;
	[self.web evaluateJavaScript:dirtyProbe completionHandler:^(id result, NSError *error) {
		MWWindow *mw = weakSelf;
		if (mw == nil) {
			return;
		}
		NSMutableArray<NSString *> *changed = [NSMutableArray array];
		if (error == nil && [result isKindOfClass:[NSArray class]]) {
			for (id name in (NSArray *)result) {
				if ([name isKindOfClass:[NSString class]]) {
					[changed addObject:name];
				}
			}
		}
		if (changed.count == 0) {
			[mw finishClose];
			return;
		}
		// The close may have come for the whole window while another tab
		// showed: bring this one forward, so the sheet is seen.
		[mw.window makeKeyAndOrderFront:nil];
		NSAlert *alert = [[NSAlert alloc] init];
		alert.alertStyle = NSAlertStyleWarning;
		alert.messageText = @"Discard unsaved changes?";
		alert.informativeText = changesText(changed);
		[alert addButtonWithTitle:@"Keep Editing"];
		[alert addButtonWithTitle:@"Discard"];
		[alert beginSheetModalForWindow:mw.window completionHandler:^(NSModalResponse response) {
			if (response == NSAlertSecondButtonReturn) {
				[mw finishClose];
			}
		}];
	}];
	return NO;
}

- (void)finishClose {
	self.closeConfirmed = YES;
	[self.window close];
}

- (void)windowWillClose:(NSNotification *)notification {
	[self.window saveFrameUsingName:frameName];
	macwinClosed((char *)self.key.UTF8String);
	self.web.navigationDelegate = nil;
	self.web.UIDelegate = nil;
	[windows removeObjectForKey:self.key];
}

@end

// MWActions is the target of menu items that act on the key window's page.
@interface MWActions : NSObject
- (void)send:(id)sender;
@end

@implementation MWActions
- (void)send:(id)sender {
	NSWindow *key = NSApp.keyWindow;
	for (MWWindow *mw in windows.allValues) {
		if (mw.window == key) {
			[mw.web evaluateJavaScript:sendForm completionHandler:nil];
			return;
		}
	}
}
@end

static MWActions *actions;

static void activate(NSWindow *w) {
	if (@available(macOS 14.0, *)) {
		[NSApp activate];
	} else {
		[NSApp activateIgnoringOtherApps:YES];
	}
	[w makeKeyAndOrderFront:nil];
	[w orderFrontRegardless];
}

static NSMenuItem *item(NSString *title, SEL action, NSString *key, NSEventModifierFlags mods) {
	NSMenuItem *mi = [[NSMenuItem alloc] initWithTitle:title action:action keyEquivalent:key];
	mi.keyEquivalentModifierMask = mods;
	return mi;
}

// installMenu gives windows the usual key equivalents (copy, select all,
// close, reload, next and previous tab), and ⌘↩ to send the question on the
// Ask page. An accessory app shows no menu bar, but the main menu still
// handles shortcuts. There is no Quit: ⌘Q in a window must not stop
// the servers memors-tray runs.
static void installMenu(void) {
	NSMenu *main = [[NSMenu alloc] init];
	NSMenuItem *appItem = [[NSMenuItem alloc] init];
	appItem.submenu = [[NSMenu alloc] initWithTitle:@"MemoRS Tray"];
	[main addItem:appItem];

	NSMenu *edit = [[NSMenu alloc] initWithTitle:@"Edit"];
	[edit addItem:item(@"Undo", @selector(undo:), @"z", NSEventModifierFlagCommand)];
	[edit addItem:item(@"Redo", @selector(redo:), @"z", NSEventModifierFlagCommand | NSEventModifierFlagShift)];
	[edit addItem:[NSMenuItem separatorItem]];
	[edit addItem:item(@"Cut", @selector(cut:), @"x", NSEventModifierFlagCommand)];
	[edit addItem:item(@"Copy", @selector(copy:), @"c", NSEventModifierFlagCommand)];
	[edit addItem:item(@"Paste", @selector(paste:), @"v", NSEventModifierFlagCommand)];
	[edit addItem:item(@"Select All", @selector(selectAll:), @"a", NSEventModifierFlagCommand)];
	[edit addItem:[NSMenuItem separatorItem]];
	actions = [[MWActions alloc] init];
	NSMenuItem *send = item(@"Send Question", @selector(send:), @"\r", NSEventModifierFlagCommand);
	send.target = actions;
	[edit addItem:send];
	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	editItem.submenu = edit;
	[main addItem:editItem];

	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	[win addItem:item(@"Reload", @selector(reload:), @"r", NSEventModifierFlagCommand)];
	[win addItem:item(@"Minimize", @selector(performMiniaturize:), @"m", NSEventModifierFlagCommand)];
	[win addItem:item(@"Close", @selector(performClose:), @"w", NSEventModifierFlagCommand)];
	[win addItem:[NSMenuItem separatorItem]];
	// The pages are tabs of one window; both usual pairs of shortcuts switch.
	NSEventModifierFlags ctrl = NSEventModifierFlagControl, shift = NSEventModifierFlagShift;
	[win addItem:item(@"Show Next Tab", @selector(selectNextTab:), @"\t", ctrl)];
	[win addItem:item(@"Show Previous Tab", @selector(selectPreviousTab:), @"\t", ctrl | shift)];
	[win addItem:item(@"Show Next Tab", @selector(selectNextTab:), @"}", NSEventModifierFlagCommand)];
	[win addItem:item(@"Show Previous Tab", @selector(selectPreviousTab:), @"{", NSEventModifierFlagCommand)];
	NSMenuItem *winItem = [[NSMenuItem alloc] init];
	winItem.submenu = win;
	[main addItem:winItem];

	NSApp.mainMenu = main;
}

void macwin_init(int debug) {
	BOOL dbg = debug != 0;
	dispatch_async(dispatch_get_main_queue(), ^{
		windows = [NSMutableDictionary dictionary];
		debugWindows = dbg;
		// No Dock icon when run outside the app bundle either.
		[NSApp setActivationPolicy:NSApplicationActivationPolicyAccessory];
		installMenu();
	});
}

// tabHost is the open window a new one joins as a tab: the one in front,
// else any; nil when there is none.
static MWWindow *tabHost(void) {
	NSWindow *front = NSApp.keyWindow;
	MWWindow *any = nil;
	for (MWWindow *mw in windows.allValues) {
		if (mw.window == front) {
			return mw;
		}
		if (any == nil || mw.window.isVisible) {
			any = mw;
		}
	}
	return any;
}

static MWWindow *newWindow(NSString *key, NSString *title) {
	WKWebViewConfiguration *conf = [[WKWebViewConfiguration alloc] init];
	conf.websiteDataStore = [WKWebsiteDataStore nonPersistentDataStore];
	conf.defaultWebpagePreferences.allowsContentJavaScript = NO;
	if (debugWindows) {
		[conf.preferences setValue:@YES forKey:@"developerExtrasEnabled"];
	}

	MWWindow *mw = [[MWWindow alloc] init];
	mw.key = key;
	mw.closeConfirmed = NO;
	mw.web = [[WKWebView alloc] initWithFrame:NSMakeRect(0, 0, 1100, 760) configuration:conf];
	mw.web.navigationDelegate = mw;
	mw.web.UIDelegate = mw;
	if (@available(macOS 13.3, *)) {
		mw.web.inspectable = debugWindows;
	}

	NSWindowStyleMask style = NSWindowStyleMaskTitled | NSWindowStyleMaskClosable |
	                          NSWindowStyleMaskMiniaturizable | NSWindowStyleMaskResizable;
	mw.window = [[NSWindow alloc] initWithContentRect:NSMakeRect(0, 0, 1100, 760)
	                                        styleMask:style
	                                          backing:NSBackingStoreBuffered
	                                            defer:NO];
	mw.window.releasedWhenClosed = NO;
	mw.window.title = title;
	mw.window.contentView = mw.web;
	mw.window.delegate = mw;
	mw.window.contentMinSize = NSMakeSize(560, 360);
	// Always a tab, whatever "Prefer tabs" says in System Settings.
	mw.window.tabbingMode = NSWindowTabbingModePreferred;
	mw.window.tabbingIdentifier = tabGroup;
	MWWindow *host = tabHost();
	if (host != nil) {
		[host.window addTabbedWindow:mw.window ordered:NSWindowAbove];
	} else {
		[mw.window center];
		[mw.window setFrameUsingName:frameName]; // where the window last was
	}
	windows[key] = mw;
	return mw;
}

void macwin_open(const char *ckey, const char *ctitle, const char *curl) {
	NSString *key = [NSString stringWithUTF8String:ckey];
	NSString *title = [NSString stringWithUTF8String:ctitle];
	NSURL *url = [NSURL URLWithString:[NSString stringWithUTF8String:curl]];
	dispatch_async(dispatch_get_main_queue(), ^{
		MWWindow *mw = windows[key] ?: newWindow(key, title);
		mw.window.title = title;
		if (url != nil) {
			[mw.web loadRequest:[NSURLRequest requestWithURL:url]];
		}
		activate(mw.window);
	});
}

void macwin_message(const char *ckey, const char *chtml) {
	NSString *key = [NSString stringWithUTF8String:ckey];
	NSString *html = [NSString stringWithUTF8String:chtml];
	dispatch_async(dispatch_get_main_queue(), ^{
		MWWindow *mw = windows[key];
		[mw.web loadHTMLString:html baseURL:nil];
	});
}
