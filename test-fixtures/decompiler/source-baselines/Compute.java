public class Compute {

  static int v() {
    return 42;
  }

  public static void main(java.lang.String[] arg0) {
    /* cappu: unsupported instruction invokestatic; the bytecode is:
     * 0: invokestatic #16
     * 3: istore_1
     * 4: iload_1
     * 5: iconst_2
     * 6: isub
     * 7: istore_2
     * 8: getstatic #22
     * 11: iload_2
     * 12: invokevirtual #28
     * 15: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }
}
