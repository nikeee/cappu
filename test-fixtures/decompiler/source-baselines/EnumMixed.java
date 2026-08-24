enum EnumMixed {
  PLUS,
  TIMES,
  IDENT;
  private final java.lang.String sym;

  private EnumMixed(java.lang.String arg2) {
    this.sym = arg2;
  }

  public int apply(int arg0, int arg1) {
    return arg0;
  }

  public java.lang.String sym() {
    return this.sym;
  }

  public static void main(java.lang.String[] arg0) {
    /* cappu: loops are not decompiled yet; the bytecode is:
     * 0: invokestatic #30
     * 3: astore_1
     * 4: iconst_0
     * 5: istore_2
     * 6: iload_2
     * 7: aload_1
     * 8: arraylength
     * 9: if_icmpge 49
     * 12: aload_1
     * 13: iload_2
     * 14: aaload
     * 15: astore_3
     * 16: getstatic #36
     * 19: aload_3
     * 20: invokevirtual #39
     * 23: aload_3
     * 24: invokevirtual #41
     * 27: aload_3
     * 28: bipush 6
     * 30: bipush 7
     * 32: invokevirtual #43
     * 35: invokedynamic #55,  0
     * 40: invokevirtual #61
     * 43: iinc 2, 1
     * 46: goto 6
     * 49: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static {
    /* cappu: unsupported instruction new; the bytecode is:
     * 0: new #80
     * 3: dup
     * 4: ldc #81
     * 6: iconst_0
     * 7: ldc #83
     * 9: invokespecial #85
     * 12: putstatic #87
     * 15: new #89
     * 18: dup
     * 19: ldc #90
     * 21: iconst_1
     * 22: ldc #92
     * 24: invokespecial #93
     * 27: putstatic #95
     * 30: new #2
     * 33: dup
     * 34: ldc #96
     * 36: iconst_2
     * 37: ldc #98
     * 39: invokespecial #99
     * 42: putstatic #101
     * 45: iconst_3
     * 46: anewarray #2
     * 49: dup
     * 50: iconst_0
     * 51: getstatic #87
     * 54: aastore
     * 55: dup
     * 56: iconst_1
     * 57: getstatic #95
     * 60: aastore
     * 61: dup
     * 62: iconst_2
     * 63: getstatic #101
     * 66: aastore
     * 67: putstatic #67
     * 70: return
     */
  }
}
