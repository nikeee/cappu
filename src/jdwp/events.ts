// Decode the body of an Event.Composite packet. One composite carries a suspend
// policy and a list of sub-events, each tagged by event kind with a kind-shaped
// payload. Only the kinds cappu requests are decoded; unknown kinds stop the
// scan (their length is not self-describing, so we cannot skip them safely).
//
// Port reference for togo/internal/jdwp/events.go.

import { ByteReader, type IdSizes } from "./idCodec.ts";
import { type Location, readLocation } from "./commands.ts";
import { EventKind } from "./protocol.ts";

export type JdwpEvent =
  | { kind: typeof EventKind.VM_START; requestId: number; thread: bigint }
  | { kind: typeof EventKind.VM_DEATH; requestId: number }
  | { kind: typeof EventKind.THREAD_START; requestId: number; thread: bigint }
  | { kind: typeof EventKind.THREAD_DEATH; requestId: number; thread: bigint }
  | { kind: typeof EventKind.BREAKPOINT; requestId: number; thread: bigint; location: Location }
  | { kind: typeof EventKind.SINGLE_STEP; requestId: number; thread: bigint; location: Location }
  | {
      kind: typeof EventKind.CLASS_PREPARE;
      requestId: number;
      thread: bigint;
      refTypeTag: number;
      typeId: bigint;
      signature: string;
      status: number;
    };

export interface Composite {
  suspendPolicy: number;
  events: JdwpEvent[];
}

export function decodeComposite(data: Buffer, sizes: IdSizes): Composite {
  const r = new ByteReader(data);
  const suspendPolicy = r.u1();
  const count = r.u4();
  const events: JdwpEvent[] = [];
  for (let i = 0; i < count; i++) {
    const kind = r.u1();
    switch (kind) {
      case EventKind.VM_START:
        events.push({ kind, requestId: r.i4(), thread: r.id(sizes.objectID) });
        break;
      case EventKind.VM_DEATH:
        events.push({ kind, requestId: r.i4() });
        break;
      case EventKind.THREAD_START:
      case EventKind.THREAD_DEATH:
        events.push({ kind, requestId: r.i4(), thread: r.id(sizes.objectID) });
        break;
      case EventKind.BREAKPOINT:
      case EventKind.SINGLE_STEP:
        events.push({
          kind,
          requestId: r.i4(),
          thread: r.id(sizes.objectID),
          location: readLocation(r, sizes),
        });
        break;
      case EventKind.CLASS_PREPARE:
        events.push({
          kind,
          requestId: r.i4(),
          thread: r.id(sizes.objectID),
          refTypeTag: r.u1(),
          typeId: r.id(sizes.referenceTypeID),
          signature: r.string(),
          status: r.i4(),
        });
        break;
      default:
        return { suspendPolicy, events }; // unknown kind: cannot skip, stop here
    }
  }
  return { suspendPolicy, events };
}
