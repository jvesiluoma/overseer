#import "CVirtualDisplay.h"

// Selectors of the private classes (shapes as used by public open-source projects such as
// DeskPad and Chromium's virtual_display_mac_util). Declared as protocols so no private class
// symbol is linked; instances come from NSClassFromString.

@protocol OVSCGVirtualDisplayDescriptor <NSObject>
@optional
- (void)setDispatchQueue:(dispatch_queue_t)queue;
- (void)setQueue:(dispatch_queue_t)queue;
@required
@property (retain, nonatomic) NSString *name;
@property (nonatomic) unsigned int maxPixelsHigh;
@property (nonatomic) unsigned int maxPixelsWide;
@property (nonatomic) CGSize sizeInMillimeters;
@property (nonatomic) unsigned int serialNum;
@property (nonatomic) unsigned int productID;
@property (nonatomic) unsigned int vendorID;
@property (copy, nonatomic) void (^terminationHandler)(id, id);
@end

@protocol OVSCGVirtualDisplayMode <NSObject>
- (instancetype)initWithWidth:(unsigned int)width height:(unsigned int)height refreshRate:(double)refreshRate;
@end

@protocol OVSCGVirtualDisplaySettings <NSObject>
@property (retain, nonatomic) NSArray *modes;
@property (nonatomic) unsigned int hiDPI;
@end

@protocol OVSCGVirtualDisplay <NSObject>
- (instancetype)initWithDescriptor:(id)descriptor;
- (BOOL)applySettings:(id)settings;
@property (readonly, nonatomic) unsigned int displayID;
@end

static Class OVSClass(NSString *name) { return NSClassFromString(name); }

@implementation OVSVirtualDisplay {
    id<OVSCGVirtualDisplay> _display;
}

+ (BOOL)isSupported {
    Class display = OVSClass(@"CGVirtualDisplay");
    return display != nil
        && OVSClass(@"CGVirtualDisplayDescriptor") != nil
        && OVSClass(@"CGVirtualDisplayMode") != nil
        && OVSClass(@"CGVirtualDisplaySettings") != nil
        && [display instancesRespondToSelector:@selector(initWithDescriptor:)]
        && [display instancesRespondToSelector:@selector(applySettings:)];
}

- (nullable instancetype)initWithName:(NSString *)name
                               serial:(uint32_t)serial
                        maxPixelsWide:(uint32_t)maxPixelsWide
                        maxPixelsHigh:(uint32_t)maxPixelsHigh
                    sizeInMillimeters:(CGSize)sizeInMillimeters {
    if (![OVSVirtualDisplay isSupported]) return nil;
    self = [super init];
    if (!self) return nil;

    id<OVSCGVirtualDisplayDescriptor> descriptor = [[OVSClass(@"CGVirtualDisplayDescriptor") alloc] init];
    // The setter name differs between macOS releases; use whichever exists.
    if ([descriptor respondsToSelector:@selector(setDispatchQueue:)]) {
        [descriptor setDispatchQueue:dispatch_get_main_queue()];
    } else if ([descriptor respondsToSelector:@selector(setQueue:)]) {
        [descriptor setQueue:dispatch_get_main_queue()];
    }
    descriptor.name = name;
    descriptor.maxPixelsWide = maxPixelsWide;
    descriptor.maxPixelsHigh = maxPixelsHigh;
    descriptor.sizeInMillimeters = sizeInMillimeters;
    descriptor.serialNum = serial;
    // Arbitrary but stable IDs so macOS remembers the arrangement per display slot.
    descriptor.vendorID = 0x4F56; // "OV"
    descriptor.productID = 0x5343 + serial;
    __weak OVSVirtualDisplay *weakSelf = self;
    descriptor.terminationHandler = ^(id unused1, id unused2) {
        OVSVirtualDisplay *strongSelf = weakSelf;
        if (strongSelf && strongSelf.terminationHandler) strongSelf.terminationHandler();
    };

    _display = [[OVSClass(@"CGVirtualDisplay") alloc] initWithDescriptor:descriptor];
    if (_display == nil || _display.displayID == 0) return nil;
    return self;
}

- (CGDirectDisplayID)displayID {
    return _display ? (CGDirectDisplayID)_display.displayID : kCGNullDirectDisplay;
}

- (BOOL)applyModeWithPixelWidth:(uint32_t)width
                    pixelHeight:(uint32_t)height
                    refreshRate:(double)refreshRate
                          hiDPI:(BOOL)hiDPI {
    if (_display == nil) return NO;
    id<OVSCGVirtualDisplaySettings> settings = [[OVSClass(@"CGVirtualDisplaySettings") alloc] init];
    settings.hiDPI = hiDPI ? 1 : 0;
    id<OVSCGVirtualDisplayMode> mode = [[OVSClass(@"CGVirtualDisplayMode") alloc] initWithWidth:width
                                                                                          height:height
                                                                                     refreshRate:refreshRate];
    if (mode == nil) return NO;
    settings.modes = @[ mode ];
    return [_display applySettings:settings];
}

- (void)invalidate {
    _display = nil;
}

- (void)dealloc {
    _display = nil;
}

@end
