export interface PetBubbleRect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export type PetBubbleTail = "tail-left" | "tail-right" | "tail-top" | "tail-bottom";

export interface PetSpeechBubblePlacement {
  left: number;
  top: number;
  tail: PetBubbleTail;
  tailX: number;
  tailY: number;
}

const MARGIN = 8;
const GAP = 12;
const PET_PADDING = 4;
const TAIL_SIZE = 12;

export function petBubbleRectsOverlap(a: PetBubbleRect, b: PetBubbleRect): boolean {
  return a.left < b.left + b.width && a.left + a.width > b.left &&
    a.top < b.top + b.height && a.top + a.height > b.top;
}

function clamp(value: number, minimum: number, maximum: number): number {
  return Math.max(minimum, Math.min(maximum, value));
}

// computeSpeechBubblePlacement keeps the typewriter card out of the pet's
// visual bounds. It tries the right side, left side, above and below, then
// falls back to the candidate with the smallest overlap on very small screens.
export function computeSpeechBubblePlacement(
  pet: PetBubbleRect,
  width: number,
  height: number,
  viewportWidth: number,
  viewportHeight: number,
): PetSpeechBubblePlacement {
  const petBox: PetBubbleRect = {
    left: pet.left - PET_PADDING,
    top: pet.top - PET_PADDING,
    width: pet.width + PET_PADDING * 2,
    height: pet.height + PET_PADDING * 2,
  };
  const candidates: Array<{left: number; top: number; tail: PetBubbleTail}> = [
    {left: pet.left + pet.width + GAP, top: pet.top + (pet.height - height) / 2, tail: "tail-left"},
    {left: pet.left - width - GAP, top: pet.top + (pet.height - height) / 2, tail: "tail-right"},
    {left: pet.left + pet.width / 2 - width / 2, top: pet.top - height - GAP, tail: "tail-bottom"},
    {left: pet.left + pet.width / 2 - width / 2, top: pet.top + pet.height + GAP, tail: "tail-top"},
  ];
  let best: {left: number; top: number; tail: PetBubbleTail} | null = null;
  let bestOverlap = Number.POSITIVE_INFINITY;
  const maxLeft = Math.max(MARGIN, viewportWidth - width - MARGIN);
  const maxTop = Math.max(MARGIN, viewportHeight - height - MARGIN);
  for (const candidate of candidates) {
    const left = clamp(candidate.left, MARGIN, maxLeft);
    const top = clamp(candidate.top, MARGIN, maxTop);
    const rect = {left, top, width, height};
    if (!petBubbleRectsOverlap(rect, petBox)) {
      best = {left, top, tail: candidate.tail};
      break;
    }
    const overlapWidth = Math.max(0, Math.min(left + width, petBox.left + petBox.width) - Math.max(left, petBox.left));
    const overlapHeight = Math.max(0, Math.min(top + height, petBox.top + petBox.height) - Math.max(top, petBox.top));
    if (overlapWidth * overlapHeight < bestOverlap) {
      bestOverlap = overlapWidth * overlapHeight;
      best = {left, top, tail: candidate.tail};
    }
  }
  const placement = best ?? {left: MARGIN, top: MARGIN, tail: "tail-bottom" as PetBubbleTail};
  const tailY = clamp(pet.top + pet.height / 2 - placement.top - TAIL_SIZE / 2, TAIL_SIZE, Math.max(TAIL_SIZE, height - TAIL_SIZE * 2));
  const tailX = clamp(pet.left + pet.width / 2 - placement.left - TAIL_SIZE / 2, TAIL_SIZE, Math.max(TAIL_SIZE, width - TAIL_SIZE * 2));
  return {...placement, tailX, tailY};
}
