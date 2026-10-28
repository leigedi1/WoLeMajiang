import { describe, expect, it, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { setActivePinia, createPinia } from 'pinia'
import TableCenter from './TableCenter.vue'
import PlayerRiver from './PlayerRiver.vue'
import ActionControls from './ActionControls.vue'
import GameResultModal from './GameResultModal.vue'
import RoomLobby from './RoomLobby.vue'
import MahjongTable from './MahjongTable.vue'
import type { SeatView, RoomStateView, ResultView } from '../protocol/types'

describe('TableCenter.vue', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })
  it('正确展示剩余牌数和阶段提示', () => {
    const wrapper = mount(TableCenter, {
      props: {
        wallLeft: 70,
        turnSeat: 0,
        mySeat: 0,
        turnStage: 'discard',
      },
    })
    expect(wrapper.text()).toContain('70')
    // 桌心只保留紧凑信息: 阶段 + 谁在行动(方位字符已移到玩家名牌)
    expect(wrapper.text()).toContain('出牌')
    expect(wrapper.text()).toContain('你')
  })
})

describe('PlayerRiver.vue', () => {
  it('弃牌河分行渲染', () => {
    const wrapper = mount(PlayerRiver, {
      props: {
        tiles: ['wan1', 'wan2', 'wan3', 'wan4', 'wan5', 'wan6', 'wan7'],
      },
    })
    const tiles = wrapper.findAllComponents({ name: 'MahjongTile' })
    expect(tiles.length).toBe(7)
  })
})

describe('ActionControls.vue', () => {
  it('渲染对应的操作按钮并触发 action 事件', async () => {
    const wrapper = mount(ActionControls, {
      props: {
        actions: ['draw', 'pong', 'pass'],
        hand: ['wan1', 'wan2'],
      },
    })

    const buttons = wrapper.findAll('button')
    const buttonTexts = buttons.map((b) => b.text())
    expect(buttonTexts).toContain('摸牌')
    expect(buttonTexts).toContain('碰')
    expect(buttonTexts).toContain('过')

    // 点击摸牌
    const drawBtn = buttons.find((b) => b.text().includes('摸牌'))
    await drawBtn?.trigger('click')
    expect(wrapper.emitted('action')?.[0]).toEqual(['draw'])

    // 点击碰
    const pongBtn = buttons.find((b) => b.text().includes('碰'))
    await pongBtn?.trigger('click')
    expect(wrapper.emitted('action')?.[1]).toEqual(['pong'])
  })

  it('吃牌单候选时直接发出动作，多候选时展示选择器', async () => {
    // 只有一个候选: 手牌有万3、万4，上家打出万5 -> 组合 [wan3, wan4]
    const wrapper = mount(ActionControls, {
      props: {
        actions: ['chow'],
        pendingTile: 'wan5',
        hand: ['wan3', 'wan4', 'tong1'],
      },
    })
    const chowBtn = wrapper.find('button')
    await chowBtn.trigger('click')
    expect(wrapper.emitted('action')?.[0]).toEqual(['chow', { with: ['wan3', 'wan4'] }])
  })

  it('多候选吃牌时在选择面板里点一项能正确发出对应的 with', async () => {
    // 手牌 3万 4万 6万 7万, 上家打 5万 → 三组: [3,4] [4,6] [6,7]
    const wrapper = mount(ActionControls, {
      attachTo: document.body,
      props: {
        actions: ['chow'],
        pendingTile: 'wan5',
        hand: ['wan3', 'wan4', 'wan6', 'wan7'],
      },
    })
    await wrapper.find('button').trigger('click')
    await nextTick()
    // 面板被 Teleport 到 body, 要从 document 里找
    const items = [...document.querySelectorAll('button')].filter((b) =>
      b.textContent?.includes('吃 五万'),
    )
    expect(items.length).toBe(3)
    // 点第二组(4万+6万), 这里正是历史上"点了无效"的场景
    items[1]!.click()
    await nextTick()
    expect(wrapper.emitted('action')?.[0]).toEqual(['chow', { with: ['wan4', 'wan6'] }])
    wrapper.unmount()
  })

  it('面板里点第一组也能正确发出(覆盖索引 0 的点击)', async () => {
    const wrapper = mount(ActionControls, {
      attachTo: document.body,
      props: {
        actions: ['chow'],
        pendingTile: 'wan5',
        hand: ['wan3', 'wan4', 'wan6', 'wan7'],
      },
    })
    await wrapper.find('button').trigger('click')
    await nextTick()
    const items = [...document.querySelectorAll('button')].filter((b) =>
      b.textContent?.includes('吃 五万'),
    )
    items[0]!.click()
    await nextTick()
    expect(wrapper.emitted('action')?.[0]).toEqual(['chow', { with: ['wan3', 'wan4'] }])
    wrapper.unmount()
  })

  it('两张暗杠候选时弹出选择面板, 点第二项发出对应的牌', async () => {
    const wrapper = mount(ActionControls, {
      attachTo: document.body,
      props: {
        actions: ['kong.concealed'],
        hand: ['wan1', 'wan1', 'wan1', 'wan1', 'tong5', 'tong5', 'tong5', 'tong5'],
      },
    })
    const kongBtn = wrapper.findAll('button').find((b) => b.text().includes('暗杠'))
    await kongBtn!.trigger('click')
    await nextTick()
    const picks = [...document.querySelectorAll('button')].filter((b) =>
      /暗杠\s*(一万|五饼)/.test(b.textContent ?? ''),
    )
    expect(picks.length).toBe(2)
    picks[1]!.click()
    await nextTick()
    expect(wrapper.emitted('action')?.[0]).toEqual(['kong.concealed', { tile: 'tong5' }])
    wrapper.unmount()
  })
})

describe('GameResultModal.vue', () => {
  it('自摸胜利时显示胜利文案与得分明细', async () => {
    const result: ResultView = {
      winner: 0,
      byDraw: true,
      fan: 3,
      pay: 8,
      winKind: 'seven_pairs',
      scores: [24, -8, -8, -8],
    }
    const wrapper = mount(GameResultModal, {
      props: {
        result,
        mySeat: 0,
        players: [
          { guestId: 'g1', nickname: '我', seat: 0, connected: true, tileCount: 14 },
          { guestId: 'g2', nickname: '乙', seat: 1, connected: true, tileCount: 13 },
        ],
      },
    })
    expect(wrapper.text()).toContain('自摸大捷')
    expect(wrapper.text()).toContain('3 番')
    expect(wrapper.text()).toContain('8 分')
    expect(wrapper.text()).toContain('+24')

    // 结算面板先给续玩入口(返回房间/再来一局), "看看牌桌" 才是关闭。
    const closeBtn = wrapper.findAll('button').find((b) => b.text().includes('看看牌桌'))
    expect(closeBtn).toBeTruthy()
    await closeBtn!.trigger('click')
    expect(wrapper.emitted('close')).toBeDefined()
  })
})

describe('RoomLobby.vue', () => {
  it('展示房间邀请码与玩家入座信息，房主拥有开局按钮', async () => {
    const room: RoomStateView = {
      roomId: 'r1',
      code: '888AAA',
      mode: 'online',
      status: 'lobby',
      theme: 'classic',
      seq: 1,
      players: [
        { guestId: 'host1', nickname: '房主', seat: 0, connected: true, tileCount: 0 },
        { guestId: 'p2', nickname: '玩家2', seat: 1, connected: true, tileCount: 0 },
      ],
    }
    const wrapper = mount(RoomLobby, {
      props: {
        room,
        myGuestId: 'host1',
      },
    })
    expect(wrapper.text()).toContain('888AAA')
    expect(wrapper.text()).toContain('房主')
    expect(wrapper.text()).toContain('玩家2')

    // 房主且人数 >= 2, 允许点击开局
    const startBtn = wrapper.find('button[class*="from-emerald-600"]')
    expect(startBtn.exists()).toBe(true)
    await startBtn.trigger('click')
    expect(wrapper.emitted('start')).toBeDefined()
  })
})

describe('MahjongTable.vue', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('渲染完整牌桌，轮到出牌时点击手牌触发 discard 动作', async () => {
    const view: SeatView = {
      seat: 0,
      phase: 'turn',
      turnSeat: 0,
      turnStage: 'discard',
      wallLeft: 80,
      hand: ['wan1', 'wan2', 'wan3'],
      drawn: 'wan3',
      melds: [],
      discards: [[], [], [], []],
      othersHandCounts: [3, 3, 3, 3],
      othersMelds: [[], [], [], []],
      actions: ['discard'],
    }
    const wrapper = mount(MahjongTable, {
      props: {
        view,
        myGuestId: 'me',
      },
    })
    expect(wrapper.text()).toContain('80') // 剩余牌数

    // 找到本家手牌区第一张牌 (wan1) 并点击
    const handTiles = wrapper.findAllComponents({ name: 'MahjongTile' })
    const myWan1 = handTiles.find((t) => t.props('code') === 'wan1')
    expect(myWan1?.exists()).toBe(true)

    await myWan1?.trigger('click')
    expect(wrapper.emitted('action')?.[0]).toEqual(['discard', { tile: 'wan1' }])
  })
})
