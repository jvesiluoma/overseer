#import <Foundation/Foundation.h>
#import <CoreGraphics/CoreGraphics.h>

NS_ASSUME_NONNULL_BEGIN

/// Thin wrapper around CoreGraphics' private CGVirtualDisplay classes.
/// All classes are looked up with NSClassFromString, so the app still launches (and reports
/// "unsupported") if Apple removes or renames them. Use from the main thread.
@interface OVSVirtualDisplay : NSObject

+ (BOOL)isSupported;

- (nullable instancetype)initWithName:(NSString *)name
                               serial:(uint32_t)serial
                        maxPixelsWide:(uint32_t)maxPixelsWide
                        maxPixelsHigh:(uint32_t)maxPixelsHigh
                    sizeInMillimeters:(CGSize)sizeInMillimeters;

- (instancetype)init NS_UNAVAILABLE;

@property (nonatomic, readonly) CGDirectDisplayID displayID;

/// Called on the main queue if the system tears the display down.
@property (nonatomic, copy, nullable) void (^terminationHandler)(void);

/// Replaces the display's mode list with a single mode of the given pixel size.
/// With hiDPI the system additionally offers a 2x ("looks like" half size) variant.
- (BOOL)applyModeWithPixelWidth:(uint32_t)width
                    pixelHeight:(uint32_t)height
                    refreshRate:(double)refreshRate
                          hiDPI:(BOOL)hiDPI;

/// Removes the display. Also happens on dealloc.
- (void)invalidate;

@end

NS_ASSUME_NONNULL_END
