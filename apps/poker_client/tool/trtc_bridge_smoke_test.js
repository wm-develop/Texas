'use strict';

const assert = require('node:assert/strict');

const calls = [];
const handlers = new Map();
const fakeInstance = {
  on(event, handler) {
    handlers.set(event, handler);
  },
  enableAudioVolumeEvaluation(interval) {
    calls.push(['volume', interval]);
  },
  async enterRoom(options) {
    calls.push(['enter', options]);
  },
  async switchRole(role) {
    calls.push(['role', role]);
  },
  async startLocalAudio() {
    calls.push(['startAudio']);
  },
  async stopLocalAudio() {
    calls.push(['stopAudio']);
  },
  async muteRemoteAudio(userId, muted) {
    calls.push(['muteRemote', userId, muted]);
  },
  async exitRoom() {
    calls.push(['exit']);
  },
  destroy() {
    calls.push(['destroy']);
  },
};

global.TRTC = {
  EVENT: {
    ERROR: 'error',
    CONNECTION_STATE_CHANGED: 'connection',
    AUDIO_VOLUME: 'volume',
    AUTOPLAY_FAILED: 'autoplay',
  },
  TYPE: {
    SCENE_LIVE: 'live',
    ROLE_AUDIENCE: 'audience',
    ROLE_ANCHOR: 'anchor',
  },
  create() {
    return fakeInstance;
  },
};

require('../web/trtc_bridge.js');

async function main() {
  const events = [];
  global.TexasTrtcBridge.setEventSink((value) => events.push(JSON.parse(value)));

  await global.TexasTrtcBridge.join(1001, 'table_9527', 'web_test', 'sig');
  const enter = calls.find((call) => call[0] === 'enter')[1];
  assert.equal(enter.strRoomId, 'table_9527');
  assert.equal(enter.scene, 'live');
  assert.equal(enter.role, 'audience');
  assert.equal(enter.autoReceiveAudio, true);

  await global.TexasTrtcBridge.setMicrophoneEnabled(true);
  assert.deepEqual(calls.slice(-2), [['role', 'anchor'], ['startAudio']]);

  handlers.get('volume')({
    result: [
      { userId: '', volume: 21 },
      { userId: 'quiet_user', volume: 3 },
      { userId: 'remote_user', volume: 64 },
    ],
  });
  assert.deepEqual(events.at(-1), {
    type: 'speaking',
    userIds: ['web_test', 'remote_user'],
  });

  await global.TexasTrtcBridge.setRemoteUserMuted('remote_user', true);
  assert.deepEqual(calls.at(-1), ['muteRemote', 'remote_user', true]);

  await global.TexasTrtcBridge.setMicrophoneEnabled(false);
  assert.deepEqual(calls.slice(-2), [['stopAudio'], ['role', 'audience']]);

  await global.TexasTrtcBridge.leave();
  assert(calls.some((call) => call[0] === 'exit'));
  assert(calls.some((call) => call[0] === 'destroy'));
  assert.equal(events.at(-1).type, 'disconnected');
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});
