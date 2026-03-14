export function createAudioCapture(options) {
  const {
    app,
    audioPlayback,
    workerSend,
    setStatus,
    appendDebug,
    onBargeIn,
  } = options;

  function flushPreRollIntoQueue() {
    for (const preFrame of app.preRollBuffer) {
      for (let i = 0; i < preFrame.length; i++) app.pcmQueue.push(preFrame[i]);
    }
    app.preRollBuffer = [];
  }

  function pushPreRollFrame(int16) {
    app.preRollBuffer.push(int16);
    if (app.preRollBuffer.length > app.preRollMaxFrames) {
      app.preRollBuffer.shift();
    }
  }

  function queuePCMFrame(int16) {
    for (let i = 0; i < int16.length; i++) app.pcmQueue.push(int16[i]);
    while (app.pcmQueue.length >= app.frameSamples16k) {
      const frame = new Int16Array(app.pcmQueue.splice(0, app.frameSamples16k));
      workerSend({ type: "send_audio", data: frame.buffer }, [frame.buffer]);
    }
  }

  function downsampleToPCM16(float32, inRate, outRate) {
    if (!float32 || float32.length === 0) return new Int16Array(0);
    if (inRate <= outRate) {
      const out = new Int16Array(float32.length);
      for (let i = 0; i < float32.length; i++) {
        const sample = Math.max(-1, Math.min(1, float32[i]));
        out[i] = sample < 0 ? sample * 32768 : sample * 32767;
      }
      return out;
    }
    const ratio = inRate / outRate;
    const outLen = Math.floor(float32.length / ratio);
    const out = new Int16Array(outLen);
    for (let i = 0; i < outLen; i++) {
      const sample = Math.max(-1, Math.min(1, float32[Math.floor(i * ratio)] || 0));
      out[i] = sample < 0 ? sample * 32768 : sample * 32767;
    }
    return out;
  }

  function handleFloatChunk(floatChunk) {
    let rms = 0;
    for (let i = 0; i < floatChunk.length; i++) rms += floatChunk[i] * floatChunk[i];
    rms = Math.sqrt(rms / Math.max(1, floatChunk.length));

    const int16 = downsampleToPCM16(floatChunk, app.sampleRateIn, 16000);
    const aiAudible = app.isPlaying || app.playbackQueue.length > 0;
    const replyOnsetGuardActive = Date.now() < app.replyOnsetGuardUntil;

    workerSend({ type: "set_ai_loud", value: aiAudible });

    if (aiAudible) {
      if (rms > app.bargeInThreshold) {
        if (app.sustainedHighRmsCount === 0) {
          audioPlayback.setVolume(0.1, 50); // Ducking
        }
        app.sustainedHighRmsCount++;
        pushPreRollFrame(int16);
      } else if (app.sustainedHighRmsCount > 0) {
        app.sustainedHighRmsCount = Math.max(0, app.sustainedHighRmsCount - 1);
        if (app.sustainedHighRmsCount === 0) {
          audioPlayback.setVolume(1.0, 300); // Recovery
        }
      }

      const canStartASR = app.sustainedHighRmsCount >= app.bargeInMinFrames;
      if (canStartASR && !app.utteranceActive) {
        const now = Date.now();
        if (now - app.lastInterruptAt > app.bargeInCooldownMs) {
          app.lastInterruptAt = now;
          app.currentTurn++;
          app.backendState = "Listening";
          setStatus("Listening");
          app.utteranceActive = true;
          app.audioSending = true;
          app.silentFramesSinceVoice = 0;
          workerSend({ type: "send_control", control: "start_listen", turn_id: app.currentTurn });
          workerSend({ type: "voice_detected" });
          flushPreRollIntoQueue();
          appendDebug("INFO", "FrontendVAD", app.currentTurn, null, "Ducking started ASR (awaiting commit)");
        }
      }

      if (app.audioSending || app.utteranceActive) {
        queuePCMFrame(int16);
      }
      return;
    } else {
      // Safety: If AI is NOT speaking, we MUST ensure volume is 100%
      // and we reset the sustained high RMS counter.
      if (app.sustainedHighRmsCount > 0) {
        app.sustainedHighRmsCount = 0;
        audioPlayback.setVolume(1.0, 100);
      }
    }

    const voiceDetected = rms > app.voiceThreshold;
    if (voiceDetected) {
      if (!app.utteranceActive && replyOnsetGuardActive) {
        pushPreRollFrame(int16);
        if (app.replyOnsetGuardLoggedTurn !== app.replyOnsetGuardTurn) {
          app.replyOnsetGuardLoggedTurn = app.replyOnsetGuardTurn;
          appendDebug("DEBUG", "FrontendVAD", app.replyOnsetGuardTurn || app.currentTurn, null, "Speech onset suppressed while awaiting reply");
        }
        return;
      }
      app.lastVoiceAt = Date.now();
      if (!app.utteranceActive) {
        app.currentTurn++;
        workerSend({ type: "send_control", control: "start_listen", turn_id: app.currentTurn });
        appendDebug("INFO", "FrontendVAD", app.currentTurn, null, "Speech onset detected (start_listen)");
      }
      app.utteranceActive = true;
      workerSend({ type: "voice_detected" });

      if (!app.audioSending) {
        app.audioSending = true;
        flushPreRollIntoQueue();
      }
      app.silentFramesSinceVoice = 0;
    } else {
      app.silentFramesSinceVoice++;
      if (app.audioSending && app.silentFramesSinceVoice > app.silentTailFrames) {
        app.audioSending = false;
        app.pcmQueue = [];
      }
    }

    if (app.audioSending || app.utteranceActive) {
      queuePCMFrame(int16);
    } else {
      pushPreRollFrame(int16);
    }
  }

  async function startMic() {
    if (app.mediaStream) return;
    app.mediaStream = await navigator.mediaDevices.getUserMedia({
      audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
    });
    const ctx = audioPlayback.ensurePlaybackContext();
    await ctx.resume();
    app.sampleRateIn = ctx.sampleRate;
    app.sourceNode = ctx.createMediaStreamSource(app.mediaStream);
    app.sinkNode = ctx.createGain();
    app.sinkNode.gain.value = 0;
    app.sinkNode.connect(ctx.destination);

    let workletOk = false;
    try {
      await audioPlayback.ensurePCMWorkletLoaded(ctx);
      app.workletNode = new AudioWorkletNode(ctx, "pcm-processor");
      app.workletNode.port.onmessage = (event) => handleFloatChunk(event.data);
      app.sourceNode.connect(app.workletNode);
      app.workletNode.connect(app.sinkNode);
      workletOk = true;
      appendDebug("INFO", "FrontendVAD", null, null, "audio path: AudioWorklet");
    } catch (err) {
      appendDebug("WARN", "FrontendVAD", null, null, `worklet fallback: ${err.message}`);
    }
    if (!workletOk) {
      app.scriptNode = ctx.createScriptProcessor(4096, 1, 1);
      app.scriptNode.onaudioprocess = (event) => handleFloatChunk(event.inputBuffer.getChannelData(0));
      app.sourceNode.connect(app.scriptNode);
      app.scriptNode.connect(app.sinkNode);
    }
  }

  function stopMic() {
    if (app.workletNode) {
      try { app.workletNode.port.onmessage = null; } catch (_) { }
      try { app.workletNode.disconnect(); } catch (_) { }
      app.workletNode = null;
    }
    if (app.scriptNode) {
      try { app.scriptNode.onaudioprocess = null; } catch (_) { }
      try { app.scriptNode.disconnect(); } catch (_) { }
      app.scriptNode = null;
    }
    if (app.sourceNode) {
      try { app.sourceNode.disconnect(); } catch (_) { }
      app.sourceNode = null;
    }
    if (app.sinkNode) {
      try { app.sinkNode.disconnect(); } catch (_) { }
      app.sinkNode = null;
    }
    if (app.mediaStream) {
      for (const track of app.mediaStream.getTracks()) track.stop();
      app.mediaStream = null;
    }
    app.pcmQueue = [];
    app.preRollBuffer = [];
    app.audioSending = false;
    app.silentFramesSinceVoice = 0;
    app.utteranceActive = false;
    app.sustainedHighRmsCount = 0;
    app.replyOnsetGuardUntil = 0;
    app.replyOnsetGuardTurn = 0;
    app.replyOnsetGuardLoggedTurn = 0;
  }

  // Safety watchdog: Ensure volume is restored if something hangs
  setInterval(() => {
    const aiAudible = app.isPlaying || app.playbackQueue.length > 0;
    if (!aiAudible && app.sustainedHighRmsCount > 0) {
      app.sustainedHighRmsCount = 0;
      audioPlayback.setVolume(1.0, 200);
      appendDebug("DEBUG", "AudioCapture", null, null, "Watchdog: Volume restored (AI not speaking)");
    }
    // Periodic check to ensure volume is 1.0 if not ducking
    if (!aiAudible && app.mainGainNode && app.mainGainNode.gain.value < 0.95 && app.sustainedHighRmsCount === 0) {
       audioPlayback.setVolume(1.0, 500);
    }
  }, 1000);

  return {
    startMic,
    stopMic,
    handleFloatChunk,
  };
}
