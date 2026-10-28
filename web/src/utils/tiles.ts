/**
 * 麻将牌元数据与无障碍定义
 * 牌值与视觉表现彻底解耦，提供 34 种标准牌的分类、全称、明文拆分及无障碍描述。
 */

export type TileSuit = 'wan' | 'tiao' | 'tong' | 'wind' | 'dragon'

export interface TileMeta {
  code: string          // 线缆代码: wan1..wan9, tiao1..tiao9, tong1..tong9, dong, nan, xi, bei, zhong, fa, bai
  name: string          // 完整中文名称，例如 "二条"、"红中"
  shortName: string     // 简短名称，例如 "2条"、"中"
  suit: TileSuit        // 种类
  rank?: number         // 数牌点数 1~9
  topText: string       // 明文主题上半字: 数牌如 "二"、字牌如 "东"
  bottomText: string    // 明文主题下半字: 数牌如 "条"、字牌如 "" 或 装饰
  color: 'red' | 'green' | 'blue' | 'black' // 主色调
  alt: string           // 无障碍朗读文本
}

export const TILES_META: Record<string, TileMeta> = {
  // --- 万子 (wan1 ~ wan9) ---
  wan1: { code: 'wan1', name: '一万', shortName: '1万', suit: 'wan', rank: 1, topText: '一', bottomText: '萬', color: 'red', alt: '一万' },
  wan2: { code: 'wan2', name: '二万', shortName: '2万', suit: 'wan', rank: 2, topText: '二', bottomText: '萬', color: 'red', alt: '二万' },
  wan3: { code: 'wan3', name: '三万', shortName: '3万', suit: 'wan', rank: 3, topText: '三', bottomText: '萬', color: 'red', alt: '三万' },
  wan4: { code: 'wan4', name: '四万', shortName: '4万', suit: 'wan', rank: 4, topText: '四', bottomText: '萬', color: 'red', alt: '四万' },
  wan5: { code: 'wan5', name: '五万', shortName: '5万', suit: 'wan', rank: 5, topText: '五', bottomText: '萬', color: 'red', alt: '五万' },
  wan6: { code: 'wan6', name: '六万', shortName: '6万', suit: 'wan', rank: 6, topText: '六', bottomText: '萬', color: 'red', alt: '六万' },
  wan7: { code: 'wan7', name: '七万', shortName: '7万', suit: 'wan', rank: 7, topText: '七', bottomText: '萬', color: 'red', alt: '七万' },
  wan8: { code: 'wan8', name: '八万', shortName: '8万', suit: 'wan', rank: 8, topText: '八', bottomText: '萬', color: 'red', alt: '八万' },
  wan9: { code: 'wan9', name: '九万', shortName: '9万', suit: 'wan', rank: 9, topText: '九', bottomText: '萬', color: 'red', alt: '九万' },

  // --- 条子 / 索子 (tiao1 ~ tiao9) ---
  tiao1: { code: 'tiao1', name: '一条', shortName: '1条', suit: 'tiao', rank: 1, topText: '一', bottomText: '条', color: 'green', alt: '一条（索子一）' },
  tiao2: { code: 'tiao2', name: '二条', shortName: '2条', suit: 'tiao', rank: 2, topText: '二', bottomText: '条', color: 'green', alt: '二条' },
  tiao3: { code: 'tiao3', name: '三条', shortName: '3条', suit: 'tiao', rank: 3, topText: '三', bottomText: '条', color: 'green', alt: '三条' },
  tiao4: { code: 'tiao4', name: '四条', shortName: '4条', suit: 'tiao', rank: 4, topText: '四', bottomText: '条', color: 'green', alt: '四条' },
  tiao5: { code: 'tiao5', name: '五条', shortName: '5条', suit: 'tiao', rank: 5, topText: '五', bottomText: '条', color: 'green', alt: '五条' },
  tiao6: { code: 'tiao6', name: '六条', shortName: '6条', suit: 'tiao', rank: 6, topText: '六', bottomText: '条', color: 'green', alt: '六条' },
  tiao7: { code: 'tiao7', name: '七条', shortName: '7条', suit: 'tiao', rank: 7, topText: '七', bottomText: '条', color: 'green', alt: '七条' },
  tiao8: { code: 'tiao8', name: '八条', shortName: '8条', suit: 'tiao', rank: 8, topText: '八', bottomText: '条', color: 'green', alt: '八条' },
  tiao9: { code: 'tiao9', name: '九条', shortName: '9条', suit: 'tiao', rank: 9, topText: '九', bottomText: '条', color: 'green', alt: '九条' },

  // --- 筒子 / 饼子 (tong1 ~ tong9) ---
  tong1: { code: 'tong1', name: '一饼', shortName: '1饼', suit: 'tong', rank: 1, topText: '一', bottomText: '饼', color: 'blue', alt: '一饼（大饼/筒子一）' },
  tong2: { code: 'tong2', name: '二饼', shortName: '2饼', suit: 'tong', rank: 2, topText: '二', bottomText: '饼', color: 'blue', alt: '二饼' },
  tong3: { code: 'tong3', name: '三饼', shortName: '3饼', suit: 'tong', rank: 3, topText: '三', bottomText: '饼', color: 'blue', alt: '三饼' },
  tong4: { code: 'tong4', name: '四饼', shortName: '4饼', suit: 'tong', rank: 4, topText: '四', bottomText: '饼', color: 'blue', alt: '四饼' },
  tong5: { code: 'tong5', name: '五饼', shortName: '5饼', suit: 'tong', rank: 5, topText: '五', bottomText: '饼', color: 'blue', alt: '五饼' },
  tong6: { code: 'tong6', name: '六饼', shortName: '6饼', suit: 'tong', rank: 6, topText: '六', bottomText: '饼', color: 'blue', alt: '六饼' },
  tong7: { code: 'tong7', name: '七饼', shortName: '7饼', suit: 'tong', rank: 7, topText: '七', bottomText: '饼', color: 'blue', alt: '七饼' },
  tong8: { code: 'tong8', name: '八饼', shortName: '8饼', suit: 'tong', rank: 8, topText: '八', bottomText: '饼', color: 'blue', alt: '八饼' },
  tong9: { code: 'tong9', name: '九饼', shortName: '9饼', suit: 'tong', rank: 9, topText: '九', bottomText: '饼', color: 'blue', alt: '九饼' },

  // --- 风牌 (四风) ---
  dong: { code: 'dong', name: '东风', shortName: '东', suit: 'wind', topText: '東', bottomText: '', color: 'black', alt: '东风' },
  nan:  { code: 'nan',  name: '南风', shortName: '南', suit: 'wind', topText: '南', bottomText: '', color: 'black', alt: '南风' },
  xi:   { code: 'xi',   name: '西风', shortName: '西', suit: 'wind', topText: '西', bottomText: '', color: 'black', alt: '西风' },
  bei:  { code: 'bei',  name: '北风', shortName: '北', suit: 'wind', topText: '北', bottomText: '', color: 'black', alt: '北风' },

  // --- 三元牌 (箭牌) ---
  zhong: { code: 'zhong', name: '红中', shortName: '中', suit: 'dragon', topText: '中', bottomText: '', color: 'red',   alt: '红中' },
  fa:    { code: 'fa',    name: '发财', shortName: '发', suit: 'dragon', topText: '發', bottomText: '', color: 'green', alt: '发财' },
  bai:   { code: 'bai',   name: '白板', shortName: '白', suit: 'dragon', topText: '白', bottomText: '板', color: 'blue',  alt: '白板' },
}

/** 获取牌完整元数据，未知代码返回容错对象 */
export function getTileMeta(code: string): TileMeta {
  if (code && TILES_META[code]) {
    return TILES_META[code]
  }
  return {
    code: code || 'unknown',
    name: code || '未知',
    shortName: code || '?',
    suit: 'wan',
    topText: code || '?',
    bottomText: '',
    color: 'black',
    alt: code || '未知牌',
  }
}

/** 获取经典主题 SVG 资源路径 */
export function getClassicTileSvgPath(code: string): string {
  if (code === 'back') return '/tiles/classic/back.svg'
  if (code === 'front') return '/tiles/classic/front.svg'
  return `/tiles/classic/${code}.svg`
}
