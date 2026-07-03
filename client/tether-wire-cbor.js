// tether-wire-cbor.js overrides Tether.decode to handle CBOR payloads.
//
// Over WebSocket, CBOR arrives as native binary frames. Over SSE (a
// text-only stream) the bytes arrive base64-encoded and are decoded
// first. Either way the CBOR decodes into a plain JS object with the
// same shape as the JSON equivalent, so the rest of tether.js works
// unchanged.
//
// Loaded automatically by the framework when WireFormat is wire.CBOR.

(function () {
  "use strict";

  // Minimal CBOR decoder covering the types tether uses:
  // unsigned int, negative int, byte string, text string, array, map,
  // boolean, null, float16/32/64. This is not a general-purpose decoder
  // but it handles every value the tether wire format produces.

  function decodeCBOR(buf) {
    var view = new DataView(buf.buffer, buf.byteOffset, buf.byteLength);
    var offset = 0;

    function read() {
      var initial = buf[offset++];
      var major = initial >> 5;
      var info = initial & 0x1f;

      var val = readArgument(info);

      switch (major) {
        case 0: return val;                          // unsigned int
        case 1: return -1 - val;                     // negative int
        case 2: return readBytes(val);               // byte string
        case 3: return readText(val);                // text string
        case 4: return readArray(val);               // array
        case 5: return readMap(val);                  // map
        case 7: return readSimpleOrFloat(info, val);  // simple/float
        default:
          throw new Error("tether-wire-cbor: unsupported major type " + major);
      }
    }

    function readArgument(info) {
      if (info < 24) return info;
      if (info === 24) return buf[offset++];
      if (info === 25) { var v = view.getUint16(offset); offset += 2; return v; }
      if (info === 26) { var v = view.getUint32(offset); offset += 4; return v; }
      if (info === 27) {
        // 64-bit: read as two 32-bit values. JS numbers lose precision
        // above 2^53 but tether payloads never hit that.
        var hi = view.getUint32(offset); offset += 4;
        var lo = view.getUint32(offset); offset += 4;
        return hi * 0x100000000 + lo;
      }
      // info 31 is indefinite length (break), handled by callers if needed.
      return info;
    }

    function readBytes(len) {
      var slice = buf.slice(offset, offset + len);
      offset += len;
      return slice;
    }

    function readText(len) {
      var slice = buf.slice(offset, offset + len);
      offset += len;
      return new TextDecoder().decode(slice);
    }

    function readArray(len) {
      var arr = new Array(len);
      for (var i = 0; i < len; i++) arr[i] = read();
      return arr;
    }

    function readMap(len) {
      // Null-prototype object: a "__proto__" key arriving on the wire
      // must become a plain own property, not a prototype write.
      var obj = Object.create(null);
      for (var i = 0; i < len; i++) {
        var key = read();
        obj[key] = read();
      }
      return obj;
    }

    function readSimpleOrFloat(info, val) {
      if (info === 20) return false;
      if (info === 21) return true;
      if (info === 22) return null;
      if (info === 23) return undefined;
      if (info === 25) {
        // float16
        var half = view.getUint16(offset - 2);
        var exp = (half >> 10) & 0x1f;
        var mant = half & 0x3ff;
        var sign = half & 0x8000 ? -1 : 1;
        if (exp === 0) return sign * 5.9604644775390625e-8 * mant;
        if (exp === 31) return mant ? NaN : sign * Infinity;
        return sign * Math.pow(2, exp - 15) * (1 + mant / 1024);
      }
      if (info === 26) {
        // float32
        var v = view.getFloat32(offset - 4);
        return v;
      }
      if (info === 27) {
        // float64
        var v = view.getFloat64(offset - 8);
        return v;
      }
      return val;
    }

    return read();
  }

  // Base64 decode helper using the browser's built-in atob.
  function base64ToBytes(str) {
    var binary = atob(str);
    var bytes = new Uint8Array(binary.length);
    for (var i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }
    return bytes;
  }

  // Override Tether.decode. WebSocket delivers CBOR as native binary
  // frames (ArrayBuffer); SSE is text-only, so its payloads arrive
  // base64-encoded and are decoded to bytes first.
  window.Tether.decode = function (data) {
    var bytes;
    if (typeof data === "string") {
      bytes = base64ToBytes(data);
    } else if (data instanceof Uint8Array) {
      bytes = data;
    } else {
      bytes = new Uint8Array(data);
    }
    return decodeCBOR(bytes);
  };
})();
