(function (global) {
  'use strict';

  const SPEAKING_VOLUME_THRESHOLD = 10;

  let eventSink = null;
  let trtc = null;
  let localUserId = '';
  let joined = false;
  let microphoneEnabled = false;
  let playbackVolume = 100;
  const remoteAudioUsers = new Set();

  function emit(type, fields) {
    if (typeof eventSink !== 'function') return;
    eventSink(JSON.stringify(Object.assign({ type: type }, fields || {})));
  }

  function requireSdk() {
    if (!global.TRTC || typeof global.TRTC.create !== 'function') {
      throw new Error('TRTC_WEB_SDK_UNAVAILABLE');
    }
    return global.TRTC;
  }

  function errorCode(error) {
    return error && Number.isFinite(error.code) ? error.code : -1;
  }

  function errorMessage(error) {
    if (!error) return 'Unknown TRTC Web error';
    return String(error.message || error.name || error);
  }

  function registerEvents(instance, sdk) {
    instance.on(sdk.EVENT.ERROR, function (error) {
      emit('error', {
        code: errorCode(error),
        message: errorMessage(error),
      });
    });

    instance.on(sdk.EVENT.CONNECTION_STATE_CHANGED, function (event) {
      if (event.state === 'CONNECTING') {
        emit(
          event.isReconnecting || event.prevState === 'CONNECTED'
            ? 'reconnecting'
            : 'connecting'
        );
      } else if (
        event.state === 'CONNECTED' ||
        event.state === 'RECONNECTED'
      ) {
        emit('connected');
      } else if (event.state === 'DISCONNECTED') {
        emit('disconnected');
      }
    });

    instance.on(sdk.EVENT.AUDIO_VOLUME, function (event) {
      const speaking = new Set();
      for (const item of event.result || []) {
        if (item.volume < SPEAKING_VOLUME_THRESHOLD) continue;
        const userId = item.userId || localUserId;
        if (userId) speaking.add(userId);
      }
      emit('speaking', { userIds: Array.from(speaking) });
    });

    instance.on(sdk.EVENT.REMOTE_USER_EXIT, function (event) {
      remoteAudioUsers.delete(event.userId || '');
    });

    instance.on(sdk.EVENT.REMOTE_AUDIO_AVAILABLE, function (event) {
      const userId = event.userId || '';
      if (!userId) return;
      remoteAudioUsers.add(userId);
      instance.setRemoteAudioVolume(userId, playbackVolume).catch(function () {});
    });

    instance.on(sdk.EVENT.REMOTE_AUDIO_UNAVAILABLE, function (event) {
      remoteAudioUsers.delete(event.userId || '');
    });

    instance.on(sdk.EVENT.AUTOPLAY_FAILED, function (event) {
      // Tencent's default autoplay dialog lets the user resume playback with
      // a click. This event is retained for diagnostics only.
      emit('autoplayFailed', { userId: event.userId || '' });
    });
  }

  async function join(sdkAppId, tableId, userId, userSig) {
    if (joined || trtc) throw new Error('TRTC_ROOM_ALREADY_JOINED');
    if (!sdkAppId || !tableId || !userId || !userSig) {
      throw new Error('TRTC_CREDENTIALS_INCOMPLETE');
    }

    const sdk = requireSdk();
    const instance = sdk.create();
    trtc = instance;
    localUserId = userId;
    registerEvents(instance, sdk);
    instance.enableAudioVolumeEvaluation(300);

    emit('connecting');
    try {
      await instance.enterRoom({
        sdkAppId: sdkAppId,
        strRoomId: tableId,
        userId: userId,
        userSig: userSig,
        scene: sdk.TYPE.SCENE_LIVE,
        role: sdk.TYPE.ROLE_AUDIENCE,
        autoReceiveAudio: true,
        enableAutoPlayDialog: true,
      });
      joined = true;
      emit('connected');
    } catch (error) {
      instance.enableAudioVolumeEvaluation(-1);
      instance.destroy();
      trtc = null;
      localUserId = '';
      emit('disconnected');
      throw error;
    }
  }

  async function leave() {
    const instance = trtc;
    if (!instance) {
      joined = false;
      microphoneEnabled = false;
      localUserId = '';
      remoteAudioUsers.clear();
      emit('speaking', { userIds: [] });
      emit('disconnected');
      return;
    }

    try {
      if (microphoneEnabled) await instance.stopLocalAudio();
      if (joined) await instance.exitRoom();
    } finally {
      instance.enableAudioVolumeEvaluation(-1);
      instance.destroy();
      trtc = null;
      joined = false;
      microphoneEnabled = false;
      localUserId = '';
      remoteAudioUsers.clear();
      emit('speaking', { userIds: [] });
      emit('disconnected');
    }
  }

  async function setMicrophoneEnabled(enabled) {
    if (!trtc || !joined) throw new Error('TRTC_ROOM_NOT_JOINED');
    if (microphoneEnabled === enabled) return;

    const sdk = requireSdk();
    if (enabled) {
      await trtc.switchRole(sdk.TYPE.ROLE_ANCHOR);
      try {
        await trtc.startLocalAudio();
        microphoneEnabled = true;
      } catch (error) {
        await trtc.switchRole(sdk.TYPE.ROLE_AUDIENCE).catch(function () {});
        throw error;
      }
    } else {
      await trtc.stopLocalAudio();
      await trtc.switchRole(sdk.TYPE.ROLE_AUDIENCE);
      microphoneEnabled = false;
    }
  }

  async function setRemoteUserMuted(userId, muted) {
    if (!trtc || !joined) throw new Error('TRTC_ROOM_NOT_JOINED');
    if (!userId) throw new Error('TRTC_REMOTE_USER_ID_EMPTY');
    await trtc.muteRemoteAudio(userId, muted);
  }

  async function setPlaybackVolume(volume) {
    playbackVolume = Math.max(0, Math.min(100, Math.round(volume * 100)));
    if (!trtc || !joined) return;
    await Promise.all(Array.from(remoteAudioUsers).map(function (userId) {
      return trtc.setRemoteAudioVolume(userId, playbackVolume);
    }));
  }

  global.TexasTrtcBridge = Object.freeze({
    setEventSink: function (sink) {
      eventSink = sink;
    },
    clearEventSink: function () {
      eventSink = null;
    },
    join: join,
    leave: leave,
    setMicrophoneEnabled: setMicrophoneEnabled,
    setRemoteUserMuted: setRemoteUserMuted,
    setPlaybackVolume: setPlaybackVolume,
  });
})(globalThis);
