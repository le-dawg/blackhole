#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>
#include <pthread.h>
#include "_cgo_export.h"

// Forward declarations of exported Go functions
void goDNSCallback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info);
void goMonitorStarted(int status);
void goMonitorStopped(void);

static void my_callback(SCDynamicStoreRef store, CFArrayRef changedKeys, void *info) {
	goDNSCallback(store, changedKeys, info);
}

static pthread_mutex_t g_monitorMutex = PTHREAD_MUTEX_INITIALIZER;
static CFRunLoopRef g_runLoop = NULL;
static volatile int g_shouldStop = 0;

int start_monitoring(const char* name) {
	CFStringRef nameStr = CFStringCreateWithCString(kCFAllocatorDefault, name, kCFStringEncodingUTF8);
	if (!nameStr) {
		goMonitorStarted(-1);
		return -1;
	}

	SCDynamicStoreContext context = {0, NULL, NULL, NULL, NULL};
	SCDynamicStoreRef store = SCDynamicStoreCreate(kCFAllocatorDefault, nameStr, my_callback, &context);
	CFRelease(nameStr);
	if (!store) {
		goMonitorStarted(-2);
		return -1;
	}

	CFStringRef pattern = CFStringCreateWithCString(kCFAllocatorDefault, "State:/Network/Global/DNS", kCFStringEncodingUTF8);
	if (!pattern) {
		CFRelease(store);
		goMonitorStarted(-3);
		return -1;
	}

	CFArrayRef keys = CFArrayCreate(kCFAllocatorDefault, (const void **)&pattern, 1, &kCFTypeArrayCallBacks);
	CFRelease(pattern);
	if (!keys) {
		CFRelease(store);
		goMonitorStarted(-4);
		return -1;
	}

	SCDynamicStoreSetNotificationKeys(store, keys, NULL);
	CFRelease(keys);

	CFRunLoopSourceRef rls = SCDynamicStoreCreateRunLoopSource(kCFAllocatorDefault, store, 0);
	CFRelease(store);
	if (!rls) {
		goMonitorStarted(-5);
		return -1;
	}

	pthread_mutex_lock(&g_monitorMutex);
	g_shouldStop = 0;
	CFRunLoopRef rl = CFRunLoopGetCurrent();
	g_runLoop = rl;
	CFRunLoopAddSource(rl, rls, kCFRunLoopCommonModes);
	pthread_mutex_unlock(&g_monitorMutex);

	// Notify Go that g_runLoop is initialized and source is added successfully
	goMonitorStarted(0);

	while (1) {
		pthread_mutex_lock(&g_monitorMutex);
		int stop = g_shouldStop;
		pthread_mutex_unlock(&g_monitorMutex);

		if (stop) {
			break;
		}

		SInt32 result = CFRunLoopRunInMode(kCFRunLoopDefaultMode, 2.0, true);
		if (result == kCFRunLoopRunFinished || result == kCFRunLoopRunStopped) {
			break;
		}
	}

	pthread_mutex_lock(&g_monitorMutex);
	CFRunLoopRemoveSource(rl, rls, kCFRunLoopCommonModes);
	CFRelease(rls);
	g_runLoop = NULL;
	pthread_mutex_unlock(&g_monitorMutex);

	goMonitorStopped();
	return 0;
}

void stop_monitoring(void) {
	pthread_mutex_lock(&g_monitorMutex);
	g_shouldStop = 1;
	if (g_runLoop) {
		CFRunLoopStop(g_runLoop);
	}
	pthread_mutex_unlock(&g_monitorMutex);
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
