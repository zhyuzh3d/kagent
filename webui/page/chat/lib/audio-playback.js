export function createAudioPlayback(options) {
  const { app, appendDebug, flashIndicator } = options;

  function ensurePlaybackContext() {
    if (!app.audioCtx) app.audioCtx = new AudioContext();
    if (!app.mainGainNode) {
      app.mainGainNode = app.audioCtx.createGain();
      app.mainGainNode.connect(app.audioCtx.destination);
    }
    return app.audioCtx;
  }

  async function ensurePCMWorkletLoaded(ctx) {
    if (app.workletModuleCtx === ctx) return;
    const code = `class PCMProcessor extends AudioWorkletProcessor {
      process(inputs) { const i = inputs[0]; if (i && i[0]) this.port.postMessage(i[0]); return true; }
    }
    if (!globalThis.__pcmProcessorRegistered) {
      registerProcessor('pcm-processor', PCMProcessor);
      globalThis.__pcmProcessorRegistered = true;
    }`;
    const blobURL = URL.createObjectURL(new Blob([code], { type: "application/javascript" }));
    try {
      await ctx.audioWorklet.addModule(blobURL);
      app.workletModuleCtx = ctx;
    } finally {
      URL.revokeObjectURL(blobURL);
    }
  }

  function playNext() {
    if (app.isPlaying) return;
    let item = app.playbackQueue.shift();
    while (item && item.epoch !== app.playbackEpoch) {
      item = app.playbackQueue.shift();
    }
    if (!item) {
      app.isPlaying = false;
      return;
    }
    app.isPlaying = true;
    const ctx = ensurePlaybackContext();
    if (ctx.state === "suspended") {
      ctx.resume().then(() => {
        if (item.format.includes("pcm")) playPCMItem(item);
        else playBlobItem(item);
      }).catch(() => {
        app.isPlaying = false;
        playNext();
      });
      return;
    }
    if (item.format.includes("pcm")) playPCMItem(item);
    else playBlobItem(item);
  }

  function playBlobItem(item) {
    const ctx = ensurePlaybackContext();
    const dataCopy = item.data.slice(0);
    const epoch = item.epoch;
    const successCb = (buffer) => {
      if (epoch !== app.playbackEpoch) {
        app.isPlaying = false;
        playNext();
        return;
      }
      const src = ctx.createBufferSource();
      src.buffer = buffer;
      src.connect(app.mainGainNode);
      app.playingBufferSource = src;
      src.onended = () => {
        if (epoch !== app.playbackEpoch) return;
        app.playingBufferSource = null;
        app.isPlaying = false;
        playNext();
      };
      src.start();
    };
    const errorCb = (err) => {
      appendDebug("ERROR", "FrontendAudio", item.turnId, null, "decodeAudioData error: " + (err ? (err.message || err) : "unknown"));
      app.isPlaying = false;
      playNext();
    };
    try {
      const promise = ctx.decodeAudioData(dataCopy, successCb, errorCb);
      if (promise) promise.catch(errorCb);
    } catch (err) {
      errorCb(err);
    }
  }

  function playPCMItem(item) {
    const ctx = ensurePlaybackContext();
    const epoch = item.epoch;
    try {
      if (epoch !== app.playbackEpoch) {
        app.isPlaying = false;
        playNext();
        return;
      }
      let data = item.data;
      if (data.byteLength % 2 !== 0) data = data.slice(0, data.byteLength - 1);
      const raw = new Int16Array(data);
      const buffer = ctx.createBuffer(1, raw.length, 24000);
      const channel = buffer.getChannelData(0);
      for (let i = 0; i < raw.length; i++) {
        channel[i] = Math.max(-1, Math.min(1, raw[i] / 32768));
      }
      const src = ctx.createBufferSource();
      src.buffer = buffer;
      src.connect(ctx.destination);
      app.playingBufferSource = src;
      src.onended = () => {
        if (epoch !== app.playbackEpoch) return;
        app.playingBufferSource = null;
        app.isPlaying = false;
        playNext();
      };
      src.start();
    } catch (err) {
      appendDebug("ERROR", "FrontendAudio", item.turnId, null, "PCM format error: " + err.message);
      app.isPlaying = false;
      playNext();
    }
  }

  function queueChunkMeta(meta) {
    app.pendingChunkMeta.push(meta);
  }

  function handleAudioBinary(buf) {
    const meta = app.pendingChunkMeta.shift();
    if (!meta) {
      appendDebug("WARN", "FrontendAudio", null, null, "binary arrived without tts_chunk metadata");
      return;
    }
    if (meta.turn_id < app.activeTurnId) {
      appendDebug("DEBUG", "FrontendAudio", meta.turn_id, null, `Stale TTS chunk dropped (active=${app.activeTurnId})`);
      return;
    }
    flashIndicator("receive");
    const format = (meta.format || "audio/mpeg").toLowerCase();
    app.playbackQueue.push({ format, data: buf, turnId: meta.turn_id, seq: meta.seq, epoch: app.playbackEpoch });
    playNext();
  }

  function stopPlayback() {
    app.playbackEpoch++;
    app.playbackQueue = [];
    app.pendingChunkMeta = [];
    app.isPlaying = false;
    if (app.playingBufferSource) {
      try {
        app.playingBufferSource.stop();
      } catch (_) {
      }
      app.playingBufferSource = null;
    }
    // Deep reset volume on stop
    setVolume(1.0, 0);
  }

  function setVolume(value, rampMs = 100) {
    const ctx = ensurePlaybackContext();
    if (!app.mainGainNode) return;
    app.mainGainNode.gain.cancelScheduledValues(ctx.currentTime);
    if (rampMs <= 0) {
      app.mainGainNode.gain.setValueAtTime(value, ctx.currentTime);
    } else {
      const timeConstant = rampMs / 1000;
      app.mainGainNode.gain.setTargetAtTime(value, ctx.currentTime, Math.max(0.01, timeConstant));
    }
  }

  function isSpeaking() {
    return !!(app.isPlaying || app.playbackQueue.length > 0);
  }

  return {
    ensurePlaybackContext,
    ensurePCMWorkletLoaded,
    queueChunkMeta,
    handleAudioBinary,
    stopPlayback,
    setVolume,
    isSpeaking,
  };
}
