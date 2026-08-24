enum EnumAbstract {
  LOW,
  HIGH;

  private EnumAbstract() {}

  public abstract int rank();

  public static void main(java.lang.String[] arg0) {
    /* cappu: unsupported instruction invokestatic; the bytecode is:
     * 0: invokestatic #22
     * 3: astore_1
     * 4: iconst_0
     * 5: istore_2
     * 6: iload_2
     * 7: aload_1
     * 8: arraylength
     * 9: if_icmpge 41
     * 12: aload_1
     * 13: iload_2
     * 14: aaload
     * 15: astore_3
     * 16: getstatic #28
     * 19: aload_3
     * 20: invokevirtual #32
     * 23: aload_3
     * 24: invokevirtual #34
     * 27: invokedynamic #46,  0
     * 32: invokevirtual #52
     * 35: iinc 2, 1
     * 38: goto 6
     * 41: return
     */
    throw new UnsupportedOperationException("cappu: not decompiled");
  }

  static {
    /* cappu: unsupported instruction new; the bytecode is:
     * 0: new #72
     * 3: dup
     * 4: ldc #73
     * 6: iconst_0
     * 7: invokespecial #74
     * 10: putstatic #76
     * 13: new #78
     * 16: dup
     * 17: ldc #79
     * 19: iconst_1
     * 20: invokespecial #80
     * 23: putstatic #82
     * 26: iconst_2
     * 27: anewarray #2
     * 30: dup
     * 31: iconst_0
     * 32: getstatic #76
     * 35: aastore
     * 36: dup
     * 37: iconst_1
     * 38: getstatic #82
     * 41: aastore
     * 42: putstatic #59
     * 45: return
     */
  }
}
