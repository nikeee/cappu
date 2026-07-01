// Test-runner API (self-contained, like src/packages/): compile src/test/java
// and run the JUnit Platform Console Launcher.

export {
  compileTests,
  compileTestsArgs,
  CONSOLE_LAUNCHER,
  consoleLauncherJar,
  findTestSources,
  jacocoAgentJar,
  mainClassesDir,
  resolveJava,
  TEST_BUILD_ROOT,
  testClassesDir,
  testRunArgs,
  testRuntimeClassPath,
} from "./testing.ts";
