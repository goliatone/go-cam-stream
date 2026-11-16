#import <Foundation/Foundation.h>
// Compile with -I pointing at the patched FFmpeg libavdevice directory.
@interface ProofDevice : NSObject
@property(copy) NSString *uniqueID;
@property(copy) NSString *localizedName;
@end
@implementation ProofDevice
@end
#include "camstream_audio_uid.h"

static ProofDevice *device(NSString *uid, NSString *name) {
    ProofDevice *d = [ProofDevice new];
    d.uniqueID = uid; d.localizedName = name;
    return d;
}
int main(void) {
    @autoreleasepool {
        ProofDevice *a = device(@"id:a space", @"Microphone");
        ProofDevice *b = device(@"id:b", @"Microphone");
        ProofDevice *c = device(@"id:c", @"Microphone Pro");
        NSCAssert(camstreamSelectAudio(@[a,b,c], a.uniqueID) == a, @"duplicate/prefix labels");
        NSCAssert(camstreamSelectAudio(@[c,b,a], a.uniqueID) == a, @"reorder");
        NSCAssert(!camstreamSelectAudio(@[a,device(a.uniqueID,@"Other")],a.uniqueID), @"duplicate UID");
        NSCAssert(!camstreamSelectAudio(@[b,c],a.uniqueID), @"removed before open");
        NSCAssert(!camstreamSelectAudio(@[device(@"replacement",a.localizedName)],a.uniqueID), @"replacement");
        for (NSString *uid in @[@"", @"id\n", @"ID:a space", [@"x" stringByPaddingToLength:1025 withString:@"x" startingAtIndex:0]]) {
            NSCAssert(!camstreamSelectAudio(@[a,b,c],uid), @"invalid or nonexact UID");
        }
        puts("PASS: exact UID, duplicate/prefix labels, reorder, duplicate/replaced/removed ID, validation");
    }
}
