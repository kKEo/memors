// Stats windows for memors-tray: one NSWindow with a WKWebView per memors-mcp
// server, living on the run loop fyne.io/systray already owns.
//
// Each window has a private, non-persistent website data store (the login
// cookie never reaches disk), content JavaScript off (the pages have none),
// and a navigation delegate that asks Go (webpolicy) about every navigation:
// the server's own pages load in the window, links elsewhere open in the
// default browser, the tray's own close link closes the window, everything
// else is refused.
//
// Closing a window (its Dismiss button, ⌘W or the close box) first asks
// whether the page has unsaved form changes. The check is a script the tray
// evaluates itself, which WebKit allows even with content JavaScript off,
// and a sheet asks before edits are thrown away.

#import <Cocoa/Cocoa.h>
#import <WebKit/WebKit.h>
#include "macwin.h"

// Implemented in Go (macwin_darwin.go).
extern int macwinDecide(char *key, char *url, int clicked);
extern void macwinClosed(char *key);

enum { decideAllow = 0, decideExternal = 1, decideDeny = 2, decideClose = 3 };

// dirtyProbe reports whether any control in a POST form on the page differs
// from the value it loaded with. GET forms (such as a search box) don't count.
static NSString *const dirtyProbe =
    @"(function(){"
     "for (const f of document.querySelectorAll('form')) {"
     "  if (f.method !== 'post') continue;"
     "  for (const el of f.elements) {"
     "    if (el.disabled) continue;"
     "    const t = (el.type || '').toLowerCase();"
     "    if (t === 'hidden' || t === 'submit' || t === 'button' || t === 'reset') continue;"
     "    if (t === 'checkbox' || t === 'radio') { if (el.checked !== el.defaultChecked) return true; continue; }"
     "    if (el.tagName === 'SELECT') { for (const o of el.options) if (o.selected !== o.defaultSelected) return true; continue; }"
     "    if ('defaultValue' in el && el.value !== el.defaultValue) return true;"
     "  }"
     "}"
     "return false;"
     "})()";

@interface MWWindow : NSObject <NSWindowDelegate, WKNavigationDelegate, WKUIDelegate>
@property (copy) NSString *key;
@property (strong) NSWindow *window;
@property (strong) WKWebView *web;
@property BOOL closeConfirmed; // the unsaved-changes check has passed; let the next close through
@end

static NSMutableDictionary<NSString *, MWWindow *> *windows;
static BOOL debugWindows;

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
		BOOL dirty = error == nil && [result isKindOfClass:[NSNumber class]] && [result boolValue];
		if (!dirty) {
			[mw finishClose];
			return;
		}
		NSAlert *alert = [[NSAlert alloc] init];
		alert.alertStyle = NSAlertStyleWarning;
		alert.messageText = @"Discard unsaved changes?";
		alert.informativeText = @"This form has changes that have not been saved.";
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
	macwinClosed((char *)self.key.UTF8String);
	self.web.navigationDelegate = nil;
	self.web.UIDelegate = nil;
	[windows removeObjectForKey:self.key];
}

@end

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
// close, reload). An accessory app shows no menu bar, but the main menu
// still handles shortcuts. There is no Quit: ⌘Q in a window must not stop
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
	NSMenuItem *editItem = [[NSMenuItem alloc] init];
	editItem.submenu = edit;
	[main addItem:editItem];

	NSMenu *win = [[NSMenu alloc] initWithTitle:@"Window"];
	[win addItem:item(@"Reload", @selector(reload:), @"r", NSEventModifierFlagCommand)];
	[win addItem:item(@"Minimize", @selector(performMiniaturize:), @"m", NSEventModifierFlagCommand)];
	[win addItem:item(@"Close", @selector(performClose:), @"w", NSEventModifierFlagCommand)];
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
	[mw.window center];
	[mw.window setFrameAutosaveName:[@"memors-tray " stringByAppendingString:key]];
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
