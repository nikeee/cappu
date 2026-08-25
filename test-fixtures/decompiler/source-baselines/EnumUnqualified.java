enum EnumUnqualified {
  A,
  B,
  C;

  private EnumUnqualified() {}

  public static void main(java.lang.String[] arg0) {
    /* cappu: loops are not decompiled yet; the bytecode is:
     * 0: getstatic #23
     * 3: invokestatic #27
     * 6: arraylength
     * 7: invokevirtual #33
     * 10: getstatic #23
     * 13: ldc #34
     * 15: invokestatic #38
     * 18: invokevirtual #42
     * 21: invokevirtual #33
     * 24: invokestatic #27
     * 27: astore_1
     * 28: iconst_0
     * 29: istore_2
     * 30: iload_2
     * 31: aload_1
     * 32: arraylength
     * 33: if_icmpge 56
     * 36: aload_1
     * 37: iload_2
     * 38: aaload
     * 39: astore_3
     * 40: getstatic #23
     * 43: aload_3
     * 44: invokevirtual #46
     * 47: invokevirtual #50
     * 50: iinc 2, 1
     * 53: goto 30
     * 56: getstatic #23
     * 59: invokevirtual #53
     * 62: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static {
    /* cappu: unsupported instruction new; the bytecode is:
     * 0: new #2
     * 3: dup
     * 4: ldc #69
     * 6: iconst_0
     * 7: invokespecial #70
     * 10: putstatic #72
     * 13: new #2
     * 16: dup
     * 17: ldc #34
     * 19: iconst_1
     * 20: invokespecial #70
     * 23: putstatic #74
     * 26: new #2
     * 29: dup
     * 30: ldc #75
     * 32: iconst_2
     * 33: invokespecial #70
     * 36: putstatic #77
     * 39: iconst_3
     * 40: anewarray #2
     * 43: dup
     * 44: iconst_0
     * 45: getstatic #72
     * 48: aastore
     * 49: dup
     * 50: iconst_1
     * 51: getstatic #74
     * 54: aastore
     * 55: dup
     * 56: iconst_2
     * 57: getstatic #77
     * 60: aastore
     * 61: putstatic #60
     * 64: return
     */
  }
}
