// Annotation-processing API (self-contained, like src/packages/): discover
// JSR-269 processors in lib/processors jars and run javac's generation pass.

export {
  discoverProcessors,
  type Exec,
  type ExecResult,
  GENERATED_ROOT,
  generatedClassesDir,
  generatedRoot,
  generatedSourcesDir,
  procOnlyArgs,
  type ProcessingResult,
  processorJars,
  runAnnotationProcessing,
} from "./processors.ts";
