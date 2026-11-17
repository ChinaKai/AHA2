import {createProfileLocks} from "./lock.mjs";

const [root, profileID] = process.argv.slice(2);
const locks = createProfileLocks(root);

const attempt = async () => {
  try {
    const lock = await locks.acquire(profileID);
    console.log(`ACQUIRED ${lock.token}`);
    await new Promise(resolve => setTimeout(resolve, 250));
    await locks.release(lock);
  } catch (error) {
    console.log(`LOCKED ${error && error.code || ""}`);
  }
  process.exit(0);
};

process.stdin.once("data", () => void attempt());
