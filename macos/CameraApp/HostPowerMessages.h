#include <IOKit/IOMessage.h>

// Swift cannot import the compound macros in IOMessage.h directly.
enum {
    CamstreamCanSystemSleep = kIOMessageCanSystemSleep,
    CamstreamSystemWillSleep = kIOMessageSystemWillSleep,
    CamstreamSystemHasPoweredOn = kIOMessageSystemHasPoweredOn,
};
