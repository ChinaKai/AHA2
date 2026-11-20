// "none" is the persisted "无形象" choice. It is a character setting, not a
// pet kill switch: the backend, autonomy and the assistant panel keep working,
// only the floating avatar and its attached bubble/thinking mark stay hidden.
export const PET_NO_CHARACTER = "none";

export function isPetNoCharacter(character: string | null | undefined): boolean {
  return String(character || "").trim().toLowerCase() === PET_NO_CHARACTER;
}

export interface PetAvatarRenderState {
  character?: string | null;
  petEnabled?: boolean;
  assistantVisible?: boolean;
}

// shouldRenderPetAvatar is the single decision for whether the floating Web
// pet (and everything anchored to it) is allowed to render.
export function shouldRenderPetAvatar(state: PetAvatarRenderState): boolean {
  if (state.assistantVisible === false) return false;
  if (state.petEnabled === false) return false;
  return !isPetNoCharacter(state.character);
}
