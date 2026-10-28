// Rebuild the silent Open Graph preview on macOS:
// swift tools/generate-og-video.swift
//
// Shows rather than tells: the logo, a search, the app sheet with the
// podcast apps, the logo again. It starts and ends on the same frame so it
// loops cleanly. The app marks come from internal/web/appicons.go.
import AppKit
import AVFoundation
import CoreText

let width = 1200
let height = 630
let fps: Int32 = 20
let frames = 140
let root = URL(fileURLWithPath: FileManager.default.currentDirectoryPath)
let fontURL = root.appendingPathComponent("web/static/fonts/hanken-grotesk-latin.woff2")
let outputURL = root.appendingPathComponent("web/static/og-video.mp4")
let iconsURL = root.appendingPathComponent("internal/web/appicons.go")

guard let fontData = try? Data(contentsOf: fontURL),
      let fontBase = (CTFontManagerCreateFontDescriptorsFromData(fontData as CFData)
          as? [CTFontDescriptor])?.first else {
    fatalError("Cannot read web/static/fonts/hanken-grotesk-latin.woff2")
}

// The sheet's apps, in the site's order; a fifth row hints at the rest.
let sheetApps = [("apple", "Apple Podcasts"), ("overcast", "Overcast"),
                 ("pocketcasts", "Pocket Casts"), ("castro", "Castro")]

guard let iconsSource = try? String(contentsOf: iconsURL, encoding: .utf8) else {
    fatalError("Cannot read internal/web/appicons.go")
}
let appIcons: [String: CGImage] = Dictionary(uniqueKeysWithValues: sheetApps.map { id, _ in
    guard let match = iconsSource.firstMatch(of: try! Regex("\"\(id)\":\\s*`(<svg.*?</svg>)`")),
          let svg = match.output[1].substring,
          let image = NSImage(data: Data(svg.replacingOccurrences(
              of: "width=\"20\" height=\"20\"", with: "width=\"112\" height=\"112\"").utf8)),
          let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else {
        fatalError("No icon for \(id) in internal/web/appicons.go")
    }
    return (id, cg)
})

let ink = CGColor(gray: 0, alpha: 1)
let muted = CGColor(gray: 0.38, alpha: 1)
let line = CGColor(gray: 0.86, alpha: 1)
let ring = CGColor(gray: 0, alpha: 0.08)
let orange = CGColor(red: 217 / 255, green: 83 / 255, blue: 11 / 255, alpha: 1)
let white = CGColor(gray: 1, alpha: 1)

func smooth(_ x: Double) -> Double {
    let x = min(1, max(0, x))
    return x * x * (3 - 2 * x)
}

func visibility(_ time: Double, start: Double, end: Double, fade: Double = 0.35) -> Double {
    smooth((time - start) / fade) * (1 - smooth((time - end) / fade))
}

func rect(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat) -> CGRect {
    CGRect(x: x, y: CGFloat(height) - y - h, width: w, height: h)
}

func point(_ x: CGFloat, _ y: CGFloat) -> CGPoint {
    CGPoint(x: x, y: CGFloat(height) - y)
}

func rounded(_ context: CGContext, _ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat,
             radius: CGFloat, fill: CGColor, stroke: CGColor? = nil, strokeWidth: CGFloat = 1) {
    let path = CGPath(roundedRect: rect(x, y, w, h), cornerWidth: radius,
                      cornerHeight: radius, transform: nil)
    context.addPath(path)
    context.setFillColor(fill)
    context.fillPath()
    if let stroke {
        context.addPath(path)
        context.setStrokeColor(stroke)
        context.setLineWidth(strokeWidth)
        context.strokePath()
    }
}

func font(_ size: CGFloat, weight: Double) -> CTFont {
    let wght = NSNumber(value: 0x7767_6874) // 'wght'
    return CTFontCreateWithFontDescriptor(
        CTFontDescriptorCreateCopyWithVariation(fontBase, wght, CGFloat(weight)), size, nil)
}

func textLine(_ value: String, size: CGFloat, color: CGColor, weight: Double) -> CTLine {
    CTLineCreateWithAttributedString(NSAttributedString(string: value, attributes: [
        .font: font(size, weight: weight),
        .foregroundColor: color,
        .kern: -size * 0.02,
    ]))
}

func textWidth(_ line: CTLine) -> CGFloat {
    CGFloat(CTLineGetTypographicBounds(line, nil, nil, nil))
}

func text(_ context: CGContext, _ value: String, x: CGFloat, baseline: CGFloat,
          size: CGFloat, color: CGColor = ink, weight: Double = 400) {
    context.textPosition = point(x, baseline)
    CTLineDraw(textLine(value, size: size, color: color, weight: weight), context)
}

// The site's RSS mark (a 24-unit icon), `size` across, top-left at (x, y).
func mark(_ context: CGContext, x: CGFloat, y: CGFloat, size: CGFloat) {
    let unit = size / 24
    context.setStrokeColor(orange)
    context.setLineWidth(2.5 * unit)
    context.setLineCap(.round)
    for radius in [9.0, 16.0] {
        context.addArc(center: point(x + 4 * unit, y + 20 * unit), radius: radius * unit,
                       startAngle: 0, endAngle: .pi / 2, clockwise: false)
        context.strokePath()
    }
    let dot = point(x + 5 * unit, y + 19 * unit)
    let r = 2.25 * unit
    context.setFillColor(orange)
    context.fillEllipse(in: CGRect(x: dot.x - r, y: dot.y - r, width: 2 * r, height: 2 * r))
}

// The site's top-right rings, drawn outward one after another.
func rings(_ context: CGContext, time: Double, start: Double) {
    let origin = point(CGFloat(width), 0)
    context.saveGState()
    for (index, radius) in [150.0, 285.0, 420.0, 555.0, 690.0].enumerated() {
        let grow = smooth((time - start - Double(index) * 0.09) / 0.6)
        guard grow > 0 else { continue }
        context.setStrokeColor(index == 0 ? orange : ring)
        context.setAlpha(grow)
        context.setLineWidth(index == 0 ? 3 : 2)
        context.addArc(center: origin, radius: radius * (0.85 + 0.15 * grow),
                       startAngle: 0, endAngle: 2 * .pi, clockwise: false)
        context.strokePath()
    }
    context.restoreGState()
}

func logoScene(_ context: CGContext, time: Double, ringStart: Double) {
    context.setFillColor(white)
    context.fill(rect(0, 0, CGFloat(width), CGFloat(height)))
    rings(context, time: time, start: ringStart)
    let word = textLine("kringkasting", size: 88, color: ink, weight: 700)
    let markSize: CGFloat = 92
    let gap: CGFloat = 22
    let x = (CGFloat(width) - markSize - gap - textWidth(word)) / 2
    mark(context, x: x, y: 254, size: markSize)
    context.textPosition = point(x + markSize + gap, 338)
    CTLineDraw(word, context)
}

func searchScene(_ context: CGContext, time: Double) {
    context.setFillColor(white)
    context.fill(rect(0, 0, CGFloat(width), CGFloat(height)))
    mark(context, x: 80, y: 66, size: 40)
    text(context, "kringkasting", x: 130, baseline: 100, size: 36, weight: 700)

    rounded(context, 80, 190, 1040, 96, radius: 18, fill: white, stroke: line, strokeWidth: 2)
    context.setStrokeColor(muted)
    context.setLineWidth(3)
    context.strokeEllipse(in: rect(116, 222, 28, 28))
    context.move(to: point(141, 247))
    context.addLine(to: point(154, 260))
    context.strokePath()
    let query = "Oppdatert"
    let typed = min(query.count, max(0, Int((time - 1.3) * 15)))
    text(context, String(query.prefix(typed)), x: 178, baseline: 252, size: 34)
    if typed < query.count, Int(time * 4) % 2 == 0 {
        let caret = 180 + textWidth(textLine(String(query.prefix(typed)), size: 34, color: ink, weight: 400))
        context.setFillColor(ink)
        context.fill(rect(caret, 220, 3, 40))
    }

    let result = smooth((time - 2.0) / 0.35)
    context.saveGState()
    context.setAlpha(result)
    let lift = 16 * (1 - result)
    rounded(context, 80, 318 + lift, 1040, 132, radius: 18, fill: white, stroke: line, strokeWidth: 2)
    rounded(context, 112, 346 + lift, 76, 76, radius: 12, fill: CGColor(gray: 0.93, alpha: 1))
    mark(context, x: 132, y: 366 + lift, size: 36)
    text(context, "Oppdatert", x: 214, baseline: 397 + lift, size: 36, weight: 700)

    // The press: the button dips, and the app sheet answers.
    let press = visibility(time, start: 2.65, end: 2.8, fade: 0.08)
    let scale = 1 - 0.04 * press
    let bw = 252 * scale, bh = 64 * scale
    let bx = 836 + (252 - bw) / 2, by = 352 + lift + (64 - bh) / 2
    rounded(context, bx, by, bw, bh, radius: 12, fill: ink)
    text(context, "Abonner i app", x: bx + 40, baseline: by + 41, size: 25, color: white, weight: 600)
    context.restoreGState()
}

// The site's app sheet: the podcast's name, then the apps, rising from below.
func sheetScene(_ context: CGContext, time: Double) {
    let rise = smooth((time - 2.8) / 0.45)
    guard rise > 0 else { return }
    context.setFillColor(CGColor(gray: 0, alpha: 0.28 * rise))
    context.fill(rect(0, 0, CGFloat(width), CGFloat(height)))

    let sheetX: CGFloat = 290, sheetW: CGFloat = 620
    let top = 100 + 540 * (1 - rise)
    let sheet = CGMutablePath()
    sheet.addRoundedRect(in: rect(sheetX, top, sheetW, 600), cornerWidth: 28, cornerHeight: 28)
    context.addPath(sheet)
    context.setFillColor(white)
    context.fillPath()

    context.saveGState()
    context.addPath(sheet)
    context.clip()
    rounded(context, sheetX + sheetW / 2 - 28, top + 14, 56, 6, radius: 3, fill: line)
    text(context, "Oppdatert", x: sheetX + 40, baseline: top + 80, size: 34, weight: 700)
    for (index, app) in sheetApps.enumerated() {
        let appear = smooth((time - 3.05 - Double(index) * 0.12) / 0.3)
        let y = top + 108 + CGFloat(index) * 112 + 14 * (1 - appear)
        // the fourth row only hints that the list goes on
        context.setAlpha(appear * (index == 3 ? 0.35 : 1))
        if index > 0 {
            context.setFillColor(CGColor(gray: 0, alpha: 0.06))
            context.fill(rect(sheetX + 40, y - 1, sheetW - 80, 1))
        }
        let icon = rect(sheetX + 40, y + 20, 72, 72)
        context.draw(appIcons[app.0]!, in: icon)
        text(context, app.1, x: sheetX + 136, baseline: y + 69, size: 38, weight: 600)
        // each of the three gets a ping in turn: any of them works
        let ping = smooth((time - 3.75 - Double(index) * 0.45) / 0.55)
        if index < 3, ping > 0, ping < 1 {
            context.setAlpha(appear * (1 - ping))
            context.setStrokeColor(orange)
            context.setLineWidth(3)
            let grow = 18 * ping
            context.addPath(CGPath(roundedRect: icon.insetBy(dx: -grow, dy: -grow),
                                   cornerWidth: 17 + grow, cornerHeight: 17 + grow, transform: nil))
            context.strokePath()
        }
        context.setAlpha(1)
    }
    context.restoreGState()
}

// Draws a scene into its own layer, so alpha set inside it stays relative.
func layer(_ context: CGContext, alpha: Double, _ draw: () -> Void) {
    guard alpha > 0 else { return }
    context.saveGState()
    context.setAlpha(alpha)
    context.beginTransparencyLayer(auxiliaryInfo: nil)
    draw()
    context.endTransparencyLayer()
    context.restoreGState()
}

func render(_ context: CGContext, time: Double) {
    context.setFillColor(white)
    context.fill(CGRect(x: 0, y: 0, width: width, height: height))

    // Frame 0 is the finished logo card, so the first frame works as a still.
    layer(context, alpha: 1 - smooth((time - 1.0) / 0.35)) {
        logoScene(context, time: time, ringStart: -1)
    }
    layer(context, alpha: visibility(time, start: 1.0, end: 5.6, fade: 0.35)) {
        searchScene(context, time: time)
        sheetScene(context, time: time)
    }
    layer(context, alpha: smooth((time - 5.6) / 0.35)) {
        logoScene(context, time: time, ringStart: 5.7)
    }
}

try? FileManager.default.removeItem(at: outputURL)
let writer = try AVAssetWriter(outputURL: outputURL, fileType: .mp4)
let input = AVAssetWriterInput(mediaType: .video, outputSettings: [
    AVVideoCodecKey: AVVideoCodecType.h264,
    AVVideoWidthKey: width,
    AVVideoHeightKey: height,
    AVVideoCompressionPropertiesKey: [
        AVVideoAverageBitRateKey: 1_200_000,
        AVVideoProfileLevelKey: AVVideoProfileLevelH264HighAutoLevel,
    ],
])
input.expectsMediaDataInRealTime = false
let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: input,
    sourcePixelBufferAttributes: [
        kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32ARGB,
        kCVPixelBufferWidthKey as String: width,
        kCVPixelBufferHeightKey as String: height,
        kCVPixelBufferCGImageCompatibilityKey as String: true,
        kCVPixelBufferCGBitmapContextCompatibilityKey as String: true,
    ])
writer.add(input)
guard writer.startWriting() else { fatalError("Writer failed: \(String(describing: writer.error))") }
writer.startSession(atSourceTime: .zero)

for frame in 0..<frames {
    while !input.isReadyForMoreMediaData { Thread.sleep(forTimeInterval: 0.01) }
    var buffer: CVPixelBuffer?
    guard let pool = adaptor.pixelBufferPool,
          CVPixelBufferPoolCreatePixelBuffer(nil, pool, &buffer) == kCVReturnSuccess,
          let buffer else { fatalError("Could not allocate frame \(frame)") }
    CVPixelBufferLockBaseAddress(buffer, [])
    guard let context = CGContext(data: CVPixelBufferGetBaseAddress(buffer), width: width,
        height: height, bitsPerComponent: 8, bytesPerRow: CVPixelBufferGetBytesPerRow(buffer),
        space: CGColorSpaceCreateDeviceRGB(),
        bitmapInfo: CGImageAlphaInfo.noneSkipFirst.rawValue | CGBitmapInfo.byteOrder32Big.rawValue) else {
        fatalError("Could not draw frame \(frame)")
    }
    render(context, time: Double(frame) / Double(fps))
    CVPixelBufferUnlockBaseAddress(buffer, [])
    guard adaptor.append(buffer, withPresentationTime: CMTime(value: Int64(frame), timescale: fps)) else {
        fatalError("Could not append frame \(frame): \(String(describing: writer.error))")
    }
}
input.markAsFinished()
let done = DispatchSemaphore(value: 0)
writer.finishWriting { done.signal() }
done.wait()
guard writer.status == .completed else { fatalError("Encode failed: \(String(describing: writer.error))") }
print(outputURL.path)
