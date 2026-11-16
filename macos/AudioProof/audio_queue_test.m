#import <Foundation/Foundation.h>
#include "camstream_audio_queue.h"
static int live;
@interface Sample : NSObject
@property int sequence;
@end
@implementation Sample
- (instancetype)init { if ((self = [super init])) live++; return self; }
- (void)dealloc { live--; [super dealloc]; }
@end
static void push(CamstreamAudioQueue *q, int sequence) {
    Sample *sample = [Sample new]; sample.sequence = sequence;
    camstreamAudioPush(q, (CFTypeRef)sample, 32);
    [sample release];
}
static void pop(CamstreamAudioQueue *q, int sequence) {
    Sample *sample = (Sample *)camstreamAudioPop(q);
    NSCAssert(sample && sample.sequence == sequence, @"FIFO ordering");
    [sample release];
}
int main(void) {
    @autoreleasepool {
        CamstreamAudioQueue q = {0};
        const int capacity = CAMSTREAM_AUDIO_QUEUE_CAPACITY;
        NSCAssert(!camstreamAudioPop(&q), @"empty queue");
        for (int i = 0; i < capacity; i++) push(&q, i);
        NSCAssert(q.count == capacity && live == capacity && !q.overflow, @"bounded retained samples");
        for (int i = 0; i < 40; i++) pop(&q, i);
        for (int i = capacity; i < (capacity+40); i++) push(&q, i);
        push(&q, (capacity+40));
        NSCAssert(q.overflow && q.count == capacity && live == capacity, @"overflow rejects without leaking");
        for (int i = 40; i < (capacity+40); i++) pop(&q, i);
        NSCAssert(q.count == 0 && live == 0 && q.overflow, @"sticky fatal overflow and release");
        q = (CamstreamAudioQueue){0};
        for (int i = 0; i < 32; i++) push(&q, i);
        camstreamAudioClear(&q);
        NSCAssert(q.count == 0 && q.bytes == 0 && live == 0, @"shutdown clears retained samples");
        Sample *large = [Sample new];
        camstreamAudioPush(&q, (CFTypeRef)large, CAMSTREAM_AUDIO_QUEUE_BYTES);
        camstreamAudioPush(&q, (CFTypeRef)large, 1);
        NSCAssert(q.overflow && q.count == 1 && q.bytes == CAMSTREAM_AUDIO_QUEUE_BYTES, @"byte budget");
        camstreamAudioClear(&q); [large release];
        NSCAssert(q.bytes == 0 && live == 0, @"byte budget cleanup");
        puts("PASS: FIFO order, wraparound, capacity, overflow, ownership and cleanup");
    }
}
