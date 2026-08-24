class LongArith {

  long add(long arg0, long arg1) {
    return arg0 + arg1;
  }

  long sub(long arg0, long arg1) {
    return arg0 - arg1;
  }

  long mul(long arg0, long arg1) {
    return arg0 * arg1;
  }

  long div(long arg0, long arg1) {
    return arg0 / arg1;
  }

  long rem(long arg0, long arg1) {
    return arg0 % arg1;
  }

  long neg(long arg0) {
    return -arg0;
  }

  long and(long arg0, long arg1) {
    return arg0 & arg1;
  }

  long or(long arg0, long arg1) {
    return arg0 | arg1;
  }

  long xor(long arg0, long arg1) {
    return arg0 ^ arg1;
  }

  long shl(long arg0, int arg1) {
    return arg0 << arg1;
  }

  long shr(long arg0, int arg1) {
    return arg0 >> arg1;
  }

  long ushr(long arg0, int arg1) {
    return arg0 >>> arg1;
  }

  long not(long arg0) {
    return arg0 ^ -1L;
  }
}
