#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>

// Forward declarations of exported Go functions
void goDNSCallback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info);
void goMonitorStarted(void);

static void my_callback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info) {
	goDNSCallback(store, changedKeys, info);
}

static CFRunLoopRef g_runLoop = NULL;

int start_monitoring(const char* name) {
	CFStringRef nameStr = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingUTF8);
	if (!nameStr) {
		goMonitorStarted();
		return -1;
	}

	SCDynamicStoreContext context = {0, NULL, NULL, NULL, NULL};
	SCDynamicStoreRef store = SCDynamicStoreCreate(kCFAllocatorDefault, nameStr, my_callback, &context);
	CFRelease(nameStr);
	if (!store) {
		goMonitorStarted();
		return -1;
	}

	CFStringRef pattern = CFStringCreateWithCString(kCFAllocatorDefault, "State:/Network/Global/DNS", kCFStringEncodingUTF8);
	if (!pattern) {
		CFRelease(store);
		goMonitorStarted();
		return -1;
	}

	CFArrayRef keys = CFArrayCreate(kCFAllocatorDefault, (const void **)&pattern, 1, &kCFTypeArrayCallBacks);
	CFRelease(pattern);
	if (!keys) {
		CFRelease(store);
		goMonitorStarted();
		return -1;
	}

	SCDynamicStoreSetNotificationKeys(store, keys, NULL);
	CFRelease(keys);

	CFRunLoopSourceRef rls = SCDynamicStoreCreateRunLoopSource(kCFAllocatorDefault, store, 0);
	CFRelease(store);
	if (!rls) {
		goMonitorStarted();
		return -1;
	}

	CFRunLoopRef rl = CFRunLoopGetCurrent();
	g_runLoop = rl;
	CFRunLoopAddSource(rl, rls, kCFRunLoopCommonModes);

	// Notify Go that g_runLoop is initialized and source is added
	goMonitorStarted();

	CFRunLoopRun();

	// Clean up after CFRunLoopRun has returned
	CFRunLoopRemoveSource(rl, rls, kCFRunLoopCommonModes);
	CFRelease(rls);

	return 0;
}

void stop_monitoring(void) {
	if (g_runLoop) {
		CFRunLoopStop(g_runLoop);
		g_runLoop = NULL;
	}
}

CFArrayRef copy_dns_servers(SCDynamicStoreRef store) {
	if (!store) return NULL;
	CFStringRef key = CFStringCreateWithCString(kCFAllocatorDefault, "State:/Network/Global/DNS", kCFStringEncodingUTF8);
	if (!key) return NULL;

	CFPropertyListRef dict = SCDynamicStoreCopyValue(store, key);
	CFRelease(key);
	if (!dict) return NULL;

	if (CFGetTypeID(dict) != CFDictionaryGetTypeID()) {
		CFRelease(dict);
		return NULL;
	}

	CFStringRef serversKey = CFStringCreateWithCString(kCFAllocatorDefault, "ServerAddresses", kCFStringEncodingUTF8);
	if (!serversKey) {
		CFRelease(dict);
		return NULL;
	}

	CFArrayRef servers = (CFArrayRef)CFDictionaryGetValue((CFDictionaryRef)dict, serversKey);
	CFRelease(serversKey);

	if (!servers || CFGetTypeID(servers) != CFArrayGetTypeID()) {
		CFRelease(dict);
		return NULL;
	}

	CFRetain(servers);
	CFRelease(dict);
	return servers;
}
